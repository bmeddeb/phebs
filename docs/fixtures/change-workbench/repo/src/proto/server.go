package proto

import searchv1 "example.invalid/workbench-closure/gen/proto/searchv1"

type Server struct{}

func (server Server) Register(registrar any) {
	searchv1.RegisterCodeSearchServer(registrar, server)
}
