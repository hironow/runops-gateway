package rpc_test

import (
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// Compile-time assertion: Dispatcher satisfies port.RPCTransport so HTTP /
// WebSocket / WebRTC adapters can depend on the port interface, not the
// concrete dispatcher type.
var _ port.RPCTransport = (*usecaserpc.Dispatcher)(nil)
