package exitcode

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestFromError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"plain error", errors.New("boom"), Failed},
		{"canceled", context.Canceled, Interrupted},
		{"wrapped canceled", fmt.Errorf("cell 3: %w", context.Canceled), Interrupted},
		{"deadline is a failure", context.DeadlineExceeded, Failed},
		{"usage", UsageError{Msg: "unknown command"}, Usage},
		{"wrapped usage", fmt.Errorf("run: %w", UsageError{Msg: "bad flag"}), Usage},
		{"budget", BudgetError{Msg: "recorded cost reached --max-cost"}, Budget},
		{"wrapped budget", fmt.Errorf("run: %w", BudgetError{Msg: "stop"}), Budget},
		{"joined usage and canceled", errors.Join(UsageError{Msg: "x"}, context.Canceled), Interrupted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromError(tt.err); got != tt.want {
				t.Fatalf("FromError(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	if OK != 0 || Failed != 1 || Usage != 2 || Budget != 3 || Interrupted != 130 {
		t.Fatalf("exit codes changed: %d %d %d %d %d", OK, Failed, Usage, Budget, Interrupted)
	}
}
