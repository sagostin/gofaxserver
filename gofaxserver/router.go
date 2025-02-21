package gofaxserver

type Router struct {
	server *Server
}

func NewRouter(server *Server) *Router {
	return &Router{server: server}
}

func (r *Router) Start() {
	go func(server *Server) {
		for {
			inboundMsg := <-server.fsInboundXFRecordCh
		}
		// todo
	}(r.server)

	go func(server *Server) {
		for {
			outboundFax := <-server.fsOutboundXFRecordCh
		}
		// todo
	}(r.server)

	// todo
}
