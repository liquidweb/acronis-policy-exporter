package main

// library funcs that are not related to any specific part

import (
	"context"
	"time"
)

// timeoutNoCancel puts a timeout on a context without a cancelFn
func timeoutNoCancel(ctx context.Context, timeout time.Duration) context.Context {
	ret, _ := context.WithTimeout(ctx, timeout)
	return ret
}
