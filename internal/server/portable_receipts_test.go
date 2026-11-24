package server

import (
	"encoding/json"
	"testing"
)

func TestCardRevisionSeparatesPositionFromMusic(t *testing.T) {
	q := userState{Current: json.RawMessage(`{"id":"a","position":39}`), Queue: []json.RawMessage{json.RawMessage(`{"id":"b"}`)}}
	m, b := queueRevisions("desktop", q)
	q.Current = json.RawMessage(`{"id":"a","position":44}`)
	m2, b2 := queueRevisions("desktop", q)
	if m != m2 || b == b2 {
		t.Fatal("bookmark changed music revision")
	}
	q.Queue = nil
	m2, _ = queueRevisions("desktop", q)
	if m2 == m {
		t.Fatal("queue edit retained music revision")
	}
	if cardHash("bad") != "" || cardHash("0000000000000000000000000000000000000000000000000000000000000000") != "66687aadf862bd776c8fc18b8e9f8e20089714856ee233b3902a591d0d5f2925" {
		t.Fatal("incompatible H2 digest")
	}
}
