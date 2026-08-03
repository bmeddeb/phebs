package orders

import ordersv1 "example.invalid/t307-neutral-service/gen/ordersv1"

// FocusedServiceNeedle is retained so the demo can prove that focused search
// includes the configured primary path.
const FocusedServiceNeedle = "T307_FOCUSED_SERVICE_NEEDLE"

type Server struct{}

func Register(registrar any) {
	ordersv1.RegisterOrdersServer(registrar, Server{})
}
