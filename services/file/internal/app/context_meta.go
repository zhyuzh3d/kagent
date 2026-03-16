package app

import "context"

type contextKey string

const contextKeyTurnID contextKey = "turn_id"

func WithTurnID(ctx context.Context, turnID uint64) context.Context {
	if ctx == nil || turnID == 0 {
		return ctx
	}
	return context.WithValue(ctx, contextKeyTurnID, turnID)
}

func TurnIDFromContext(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	v := ctx.Value(contextKeyTurnID)
	if v == nil {
		return 0
	}
	id, ok := v.(uint64)
	if !ok {
		return 0
	}
	return id
}
