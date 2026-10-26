package mediajobs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusWaiting     Status = "waiting"
	StatusInteractive Status = "interactive"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
)

type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	data  []byte
	msgID int
}

type Result struct {
	StreamURL         string `json:"streamUrl,omitempty"`
	DownloadURL       string `json:"downloadUrl,omitempty"`
	VLCURL            string `json:"vlcUrl,omitempty"`
	WebVideoCasterURL string `json:"webVideoCasterUrl,omitempty"`
	ExpiresAt         string `json:"expiresAt,omitempty"`
}

type Job struct {
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Choices   []Choice  `json:"choices,omitempty"`
	Result    *Result   `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	SourceBot string    `json:"-"`
	Payload   string    `json:"-"`
	peer      tg.InputPeerClass
	peerID    int64
}

type Manager struct {
	mu      sync.RWMutex
	client  *gotgproto.Client
	log     *zap.Logger
	jobs    map[string]*Job
	active  *Job
	allowed map[string]struct{}
}

var Default = &Manager{jobs: map[string]*Job{}, allowed: map[string]struct{}{}}

func Enabled() bool {
	return config.ValueOf.CineRateBridgeSecret != "" && len(config.ValueOf.CineRateBridgeSecret) >= 32 && config.ValueOf.UserSession != "" && config.ValueOf.CineRateSourceBots != ""
}

func Attach(client *gotgproto.Client, log *zap.Logger) {
	if !Enabled() {
		log.Info("CineRate media-link bridge disabled")
		return
	}
	Default.mu.Lock()
	Default.client = client
	Default.log = log.Named("CINERATE_MEDIA_JOBS")
	for _, username := range strings.Split(config.ValueOf.CineRateSourceBots, ",") {
		username = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(username, "@")))
		if username != "" {
			Default.allowed[username] = struct{}{}
		}
	}
	Default.mu.Unlock()
	client.Dispatcher.AddHandler(handlers.NewAnyUpdate(Default.handleUpdate))
	Default.log.Info("CineRate media-link bridge enabled", zap.Int("sourceBots", len(Default.allowed)))
}

func (m *Manager) Create(id, sourceBot, payload string) (*Job, error) {
	if !Enabled() || m.client == nil {
		return nil, errors.New("media-link bridge is disabled")
	}
	sourceBot = strings.TrimSpace(strings.TrimPrefix(sourceBot, "@"))
	if _, ok := m.allowed[strings.ToLower(sourceBot)]; !ok {
		return nil, errors.New("source bot is not allowed")
	}
	if id == "" || payload == "" || len(payload) > 64 {
		return nil, errors.New("invalid media-link job")
	}
	now := time.Now().UTC()
	job := &Job{ID: id, SourceBot: sourceBot, Payload: payload, Status: StatusQueued, Message: "Waiting for Telegram", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(2 * time.Hour)}
	m.mu.Lock()
	if _, exists := m.jobs[id]; exists {
		m.mu.Unlock()
		return nil, errors.New("job already exists")
	}
	m.jobs[id] = job
	shouldStart := m.active == nil
	if shouldStart {
		m.active = job
	}
	m.mu.Unlock()
	if shouldStart {
		go m.start(job)
	}
	return cloneJob(job), nil
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	if !ok || time.Now().After(job.ExpiresAt) {
		return nil, false
	}
	return cloneJob(job), true
}

func (m *Manager) Cancel(id string) (*Job, bool) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	if job.Status != StatusCompleted && job.Status != StatusFailed {
		job.Status, job.Message, job.UpdatedAt = StatusCancelled, "Cancelled", time.Now().UTC()
	}
	wasActive := m.active == job
	if wasActive {
		m.active = nil
	}
	result := cloneJob(job)
	m.mu.Unlock()
	if wasActive {
		m.startNext()
	}
	return result, true
}

func (m *Manager) Choose(id, choiceID string) (*Job, error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok || m.active != job || job.Status != StatusInteractive {
		m.mu.Unlock()
		return nil, errors.New("job is not waiting for a choice")
	}
	var choice *Choice
	for i := range job.Choices {
		if job.Choices[i].ID == choiceID {
			choice = &job.Choices[i]
			break
		}
	}
	if choice == nil {
		m.mu.Unlock()
		return nil, errors.New("choice is no longer available")
	}
	peer := job.peer
	selectedChoice := *choice
	selectedChoice.data = append([]byte(nil), choice.data...)
	job.Status, job.Message, job.Choices, job.UpdatedAt = StatusWaiting, "Waiting for Telegram", nil, time.Now().UTC()
	choiceStartedAt := job.UpdatedAt
	result := cloneJob(job)
	m.mu.Unlock()
	go m.invokeChoice(job, peer, selectedChoice, choiceStartedAt)
	return result, nil
}

func (m *Manager) invokeChoice(job *Job, peer tg.InputPeerClass, choice Choice, choiceStartedAt time.Time) {
	_, err := m.client.API().MessagesGetBotCallbackAnswer(context.Background(), &tg.MessagesGetBotCallbackAnswerRequest{Peer: peer, MsgID: choice.msgID, Data: choice.data})
	if err == nil {
		return
	}

	m.mu.RLock()
	progressed := m.active != job || job.Status == StatusInteractive || job.Status == StatusCompleted || job.Status == StatusCancelled || job.UpdatedAt.After(choiceStartedAt)
	m.mu.RUnlock()
	if progressed || isNonFatalCallbackError(err) {
		if m.log != nil {
			m.log.Warn("Telegram callback acknowledgement failed; continuing to wait for updates", zap.String("jobId", job.ID), zap.Error(err))
		}
		return
	}

	m.fail(job, err)
}

func isNonFatalCallbackError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "TIMEOUT") || strings.Contains(message, "TIMED OUT") || strings.Contains(message, "DEADLINE EXCEEDED")
}

func (m *Manager) start(job *Job) {
	ctx := m.client.CreateContext()
	resolved, err := ctx.ResolveUsername(job.SourceBot)
	if err != nil {
		m.fail(job, err)
		return
	}
	peer := resolved.GetInputPeer()
	m.mu.Lock()
	job.peer = peer
	job.peerID = resolved.GetID()
	job.Status, job.Message, job.UpdatedAt = StatusWaiting, "Waiting for Telegram", time.Now().UTC()
	m.mu.Unlock()
	randomID, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	_, err = m.client.API().MessagesSendMessage(context.Background(), &tg.MessagesSendMessageRequest{Peer: peer, Message: "/start " + job.Payload, RandomID: randomID.Int64()})
	if err != nil {
		m.fail(job, err)
		return
	}
	go m.expireActive(job, 3*time.Minute)
}

func (m *Manager) handleUpdate(ctx *ext.Context, update *ext.Update) error {
	m.mu.RLock()
	job := m.active
	m.mu.RUnlock()
	if job == nil || update.EffectiveMessage == nil || update.EffectiveChat() == nil || update.EffectiveChat().GetID() != job.peerID {
		return nil
	}
	message := update.EffectiveMessage
	if time.Unix(int64(message.Date), 0).Before(job.CreatedAt.Add(-5 * time.Second)) {
		return nil
	}
	if _, isDocument := message.Media.(*tg.MessageMediaDocument); isDocument {
		if _, err := utils.FileFromMedia(message.Media); err == nil {
			go m.complete(job, message.Message)
			return nil
		}
	}
	markup, ok := message.ReplyMarkup.(*tg.ReplyInlineMarkup)
	if !ok || len(markup.Rows) == 0 {
		return nil
	}
	choices := make([]Choice, 0)
	for _, row := range markup.Rows {
		for _, button := range row.Buttons {
			callback, ok := button.(*tg.KeyboardButtonCallback)
			if !ok {
				continue
			}
			choices = append(choices, Choice{ID: opaqueChoiceID(), Label: callback.Text, data: append([]byte(nil), callback.Data...), msgID: message.ID})
		}
	}
	if len(choices) == 0 {
		return nil
	}
	m.mu.Lock()
	if m.active == job && job.Status != StatusCancelled {
		job.Status, job.Message, job.Choices, job.UpdatedAt = StatusInteractive, message.Message.Message, choices, time.Now().UTC()
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) complete(job *Job, message *tg.Message) {
	m.mu.RLock()
	stillActive := m.active == job && job.Status != StatusCancelled
	m.mu.RUnlock()
	if !stillActive {
		return
	}
	fromPeer := job.peer
	toChannel, err := utils.GetLogChannelPeer(context.Background(), m.client.API(), m.client.PeerStorage)
	if err != nil {
		m.fail(job, err)
		return
	}
	randomID, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	updates, err := m.client.API().MessagesForwardMessages(context.Background(), &tg.MessagesForwardMessagesRequest{RandomID: []int64{randomID.Int64()}, FromPeer: fromPeer, ID: []int{message.ID}, ToPeer: &tg.InputPeerChannel{ChannelID: toChannel.ChannelID, AccessHash: toChannel.AccessHash}})
	if err != nil {
		m.fail(job, err)
		return
	}
	messageID, err := forwardedMessageID(updates)
	if err != nil {
		m.fail(job, err)
		return
	}
	file, err := utils.FileFromMedia(message.Media)
	if err != nil {
		m.fail(job, err)
		return
	}
	expires := time.Now().UTC().Add(7 * 24 * time.Hour)
	signature := utils.SignFile(file.FileName, file.FileSize, file.MimeType, file.ID, expires.Unix())
	streamURL := fmt.Sprintf("%s/stream/%d?signature=%s&expires=%d", strings.TrimRight(config.ValueOf.Host, "/"), messageID, signature, expires.Unix())
	playerURL := func(player string) string {
		query := url.Values{"video": {fmt.Sprint(messageID)}, "expires": {fmt.Sprint(expires.Unix())}, "signature": {utils.SignPlayerLaunch(player, messageID, expires.Unix())}}
		return fmt.Sprintf("%s/player/%s?%s", strings.TrimRight(config.ValueOf.Host, "/"), player, query.Encode())
	}
	m.mu.Lock()
	if m.active == job && job.Status != StatusCancelled {
		job.Status, job.Message, job.Choices, job.UpdatedAt = StatusCompleted, "Video link ready", nil, time.Now().UTC()
		job.Result = &Result{StreamURL: streamURL, DownloadURL: streamURL + "&d=true", VLCURL: playerURL("vlc"), WebVideoCasterURL: playerURL("wvc"), ExpiresAt: expires.Format(time.RFC3339)}
		m.active = nil
	}
	m.mu.Unlock()
	m.startNext()
}

func (m *Manager) expireActive(job *Job, after time.Duration) {
	timer := time.NewTimer(after)
	defer timer.Stop()
	<-timer.C
	m.mu.RLock()
	timedOut := m.active == job && (job.Status == StatusWaiting || job.Status == StatusInteractive)
	m.mu.RUnlock()
	if timedOut {
		m.fail(job, errors.New("Telegram source bot did not finish within the allowed time"))
	}
}

func (m *Manager) fail(job *Job, err error) {
	m.mu.Lock()
	if job.Status != StatusCancelled {
		job.Status, job.Message, job.Error, job.UpdatedAt = StatusFailed, "Telegram request failed", err.Error(), time.Now().UTC()
	}
	if m.active == job {
		m.active = nil
	}
	m.mu.Unlock()
	if m.log != nil {
		m.log.Warn("Media-link job failed", zap.String("jobId", job.ID), zap.Error(err))
	}
	m.startNext()
}

func (m *Manager) startNext() {
	m.mu.Lock()
	if m.active != nil {
		m.mu.Unlock()
		return
	}
	var next *Job
	for _, candidate := range m.jobs {
		if candidate.Status == StatusQueued && (next == nil || candidate.CreatedAt.Before(next.CreatedAt)) {
			next = candidate
		}
	}
	if next != nil {
		m.active = next
	}
	m.mu.Unlock()
	if next != nil {
		go m.start(next)
	}
}

func cloneJob(job *Job) *Job {
	copy := *job
	copy.Choices = append([]Choice(nil), job.Choices...)
	return &copy
}

func opaqueChoiceID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

func forwardedMessageID(updates tg.UpdatesClass) (int, error) {
	container, ok := updates.(*tg.Updates)
	if !ok {
		return 0, errors.New("unexpected Telegram forward response")
	}
	for _, update := range container.Updates {
		if mapped, ok := update.(*tg.UpdateMessageID); ok {
			return mapped.ID, nil
		}
	}
	return 0, errors.New("forwarded Telegram message ID is missing")
}
