package external

import ordersv1 "example.invalid/t307-neutral-service/gen/ordersv1"

// CreateOrder is deliberately outside the focused search shard. The complete
// repository caller overlay must still retain and surface this exact caller.
func CreateOrder(
	ctx any,
	client ordersv1.OrdersClient,
	request any,
) (any, error) {
	return client.Create(ctx, request)
}
