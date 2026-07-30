package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"EverythingSuckz/fsb/config"
)

type signedFile struct {
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
	FileID   int64  `json:"file_id"`
	Expires  int64  `json:"expires"`
}

func SignFile(fileName string, fileSize int64, mimeType string, fileID int64, expires int64) string {
	payload, _ := json.Marshal(signedFile{
		FileName: fileName,
		FileSize: fileSize,
		MimeType: mimeType,
		FileID:   fileID,
		Expires:  expires,
	})

	mac := hmac.New(sha256.New, []byte(config.ValueOf.LinkSigningKey))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func CheckSignature(inputSignature string, expectedSignature string) bool {
	input, err := hex.DecodeString(inputSignature)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(expectedSignature)
	if err != nil {
		return false
	}
	return hmac.Equal(input, expected)
}
