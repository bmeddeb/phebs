package lib

import "example.test/external/pkg"

func Value() int { return pkg.Value() + variant() }
