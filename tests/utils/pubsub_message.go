//go:build integration

package testutils

import (
	gpubsub "cloud.google.com/go/pubsub/v2"

	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
)

// MsgAdapter adapts *gpubsub.Message to pubsubinput.Message (the interface the
// Receiver / OutboundReceiver consume). It replaces the duplicated
// pubsubMsgAdapter / outboundMsgAdapter previously defined inline in each
// integration test file.
type MsgAdapter struct{ Inner *gpubsub.Message }

func (m MsgAdapter) ID() string                    { return m.Inner.ID }
func (m MsgAdapter) Data() []byte                  { return m.Inner.Data }
func (m MsgAdapter) Attributes() map[string]string { return m.Inner.Attributes }
func (m MsgAdapter) Ack()                          { m.Inner.Ack() }
func (m MsgAdapter) Nack()                         { m.Inner.Nack() }

// Compile-time assertion that MsgAdapter satisfies the receiver's Message port.
var _ pubsubinput.Message = MsgAdapter{}
