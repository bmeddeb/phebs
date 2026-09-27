#include "textflag.h"

TEXT ·Answer(SB), NOSPLIT, $0-8
	MOVD $42, R0
	MOVD R0, ret+0(FP)
	RET
