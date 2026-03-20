package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"kagent/pkg/toolproto"
)

func responseMeta(ctx *toolproto.Context, serviceID string, instanceID string) toolproto.Meta {
	meta := toolproto.Meta{
		ServiceID:  strings.TrimSpace(serviceID),
		InstanceID: strings.TrimSpace(instanceID),
	}
	if ctx != nil {
		meta.RequestID = strings.TrimSpace(ctx.RequestID)
		meta.TraceID = strings.TrimSpace(ctx.TraceID)
	}
	return meta
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
