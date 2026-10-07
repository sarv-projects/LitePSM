package trust

import (
	"context"
	"time"
)

// withTimeout bounds a verifier subprocess call.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 60*time.Second)
}
