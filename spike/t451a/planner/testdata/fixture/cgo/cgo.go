package cgo

/* static int neutral_value(void) { return 9; } */
import "C"

func Value() int { return int(C.neutral_value()) }
