package app

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"
)

func TestIsExpectedHubASRFinishClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "normal closure",
			err:  &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "normal"},
			want: true,
		},
		{
			name: "abnormal closure after finish",
			err:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "unexpected EOF"},
			want: true,
		},
		{
			name: "unexpected eof string",
			err:  errors.New("websocket: close 1006 (abnormal closure): unexpected EOF"),
			want: true,
		},
		{
			name: "vendor finish last sequence",
			err:  errors.New("remote closed: finish last sequence"),
			want: true,
		},
		{
			name: "non-finish error",
			err:  errors.New("read asr ws: i/o timeout"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExpectedHubASRFinishClose(tc.err); got != tc.want {
				t.Fatalf("isExpectedHubASRFinishClose(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
