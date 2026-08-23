package t4013

import "errors"

const maxInt64Value = int64(^uint64(0) >> 1)

var errArithmeticOverflow = errors.New("T40.13 arithmetic overflow")

func checkedAddInt64(left, right int64) (int64, error) {
	if right > 0 && left > maxInt64Value-right {
		return 0, errArithmeticOverflow
	}
	if right < 0 && left < -maxInt64Value-1-right {
		return 0, errArithmeticOverflow
	}
	return left + right, nil
}

func checkedMulInt64(left, right int64) (int64, error) {
	if left == 0 || right == 0 {
		return 0, nil
	}
	if left == -1 && right == -maxInt64Value-1 || right == -1 && left == -maxInt64Value-1 {
		return 0, errArithmeticOverflow
	}
	if left > 0 {
		if right > 0 && left > maxInt64Value/right {
			return 0, errArithmeticOverflow
		}
		if right < 0 && right < (-maxInt64Value-1)/left {
			return 0, errArithmeticOverflow
		}
		return left * right, nil
	}
	if right > 0 && left < (-maxInt64Value-1)/right {
		return 0, errArithmeticOverflow
	}
	if left < 0 && right < 0 && left < maxInt64Value/right {
		return 0, errArithmeticOverflow
	}
	return left * right, nil
}

func checkedSumInt64(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		var err error
		total, err = checkedAddInt64(total, value)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func checkedAddInt(left, right int) (int, error) {
	sum, err := checkedAddInt64(int64(left), int64(right))
	if err != nil || int64(int(sum)) != sum {
		return 0, errArithmeticOverflow
	}
	return int(sum), nil
}

func checkedMulInt(left, right int) (int, error) {
	product, err := checkedMulInt64(int64(left), int64(right))
	if err != nil || int64(int(product)) != product {
		return 0, errArithmeticOverflow
	}
	return int(product), nil
}

func checkedEqualIntSum(left, right, want int64) bool {
	sum, err := checkedAddInt64(left, right)
	return err == nil && sum == want
}
