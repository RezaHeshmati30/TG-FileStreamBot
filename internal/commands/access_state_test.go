package commands

import "testing"

func TestAccessStateRemainsBackwardCompatible(t *testing.T) {
	state, ok := decodeAccessState(accessStatePrefix + `{"users":[123],"actor_id":1,"updated_at":1}`)
	if !ok {
		t.Fatal("expected legacy access state to decode")
	}
	if len(usernamesFromState(state)) != 0 {
		t.Fatal("legacy state should not invent usernames")
	}
	if len(subtitleLanguagesFromState(state)) != 0 {
		t.Fatal("legacy state should not invent subtitle languages")
	}
}

func TestAccessStateStoresUsernames(t *testing.T) {
	users := map[int64]struct{}{123: {}}
	encoded, err := encodeAccessState(users, map[int64]string{123: "maxmustermann"}, map[int64]string{123: "german"}, 1)
	if err != nil {
		t.Fatalf("encode access state: %v", err)
	}
	state, ok := decodeAccessState(encoded)
	if !ok {
		t.Fatal("expected encoded access state to decode")
	}
	if username := usernamesFromState(state)[123]; username != "maxmustermann" {
		t.Fatalf("expected stored username, got %q", username)
	}
	if language := subtitleLanguagesFromState(state)[123]; language != "german" {
		t.Fatalf("expected stored subtitle language, got %q", language)
	}
}
