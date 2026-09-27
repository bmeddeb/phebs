//go:build !linux

package typedsandbox

import "context"

func BeginAllowance(context.Context, string, string) (Allowance, error) {
	return Allowance{}, ErrRefused
}
func (Allowance) CheckLive(context.Context) error { return ErrRefused }
func AllowanceContext(context.Context, Allowance) (context.Context, context.CancelFunc, error) {
	return nil, nil, ErrRefused
}
