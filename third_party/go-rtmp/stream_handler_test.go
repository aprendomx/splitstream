//
// Copyright (c) 2018- yutopp (yutopp@gmail.com)
//
// Distributed under the Boost Software License, Version 1.0. (See accompanying
// file LICENSE_1_0.txt or copy at  https://www.boost.org/LICENSE_1_0.txt)
//

package rtmp

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yutopp/go-rtmp/message"
)

func TestStreamHandlerChangeState(t *testing.T) {
	rwc := &rwcMock{}
	c := newConn(rwc, nil)
	s := newStream(42, c)

	s.handler.ChangeState(streamStateUnknown)
	require.Equal(t, s.handler.state, streamStateUnknown)
	require.Equal(t, s.handler.handler, nil)

	s.handler.ChangeState(streamStateServerNotConnected)
	require.Equal(t, s.handler.state, streamStateServerNotConnected)
	require.Equal(t, s.handler.handler, &serverControlNotConnectedHandler{sh: s.handler})

	s.handler.ChangeState(streamStateServerConnected)
	require.Equal(t, s.handler.state, streamStateServerConnected)
	require.Equal(t, s.handler.handler, &serverControlConnectedHandler{sh: s.handler})

	s.handler.ChangeState(streamStateServerInactive)
	require.Equal(t, s.handler.state, streamStateServerInactive)
	require.Equal(t, s.handler.handler, &serverDataInactiveHandler{sh: s.handler})

	s.handler.ChangeState(streamStateServerPublish)
	require.Equal(t, s.handler.state, streamStateServerPublish)
	require.Equal(t, s.handler.handler, &serverDataPublishHandler{sh: s.handler})

	s.handler.ChangeState(streamStateServerPlay)
	require.Equal(t, s.handler.state, streamStateServerPlay)
	require.Equal(t, s.handler.handler, &serverDataPlayHandler{sh: s.handler})

	s.handler.ChangeState(streamStateClientNotConnected)
	require.Equal(t, s.handler.state, streamStateClientNotConnected)
	require.Equal(t, s.handler.handler, &clientControlNotConnectedHandler{sh: s.handler})
}

func TestStreamStateString(t *testing.T) {
	require.Equal(t, "<Unknown>", streamStateUnknown.String())
	require.Equal(t, "NotConnected(Server)", streamStateServerNotConnected.String())
	require.Equal(t, "Connected(Server)", streamStateServerConnected.String())
	require.Equal(t, "Inactive(Server)", streamStateServerInactive.String())
	require.Equal(t, "Publish(Server)", streamStateServerPublish.String())
	require.Equal(t, "Play(Server)", streamStateServerPlay.String())
	require.Equal(t, "NotConnected(Client)", streamStateClientNotConnected.String())
	require.Equal(t, "Connected(Client)", streamStateClientConnected.String())
}

// TestStreamHandlerIgnoresAResponseToAnUnknownTransaction: a _result or _error for a
// transaction this side never registered is dropped with a debug line, not turned into an
// error. handleCommand's error reaches Conn.handleMessage, which is not prepared to
// swallow it, so returning one here tears the whole connection down — and a peer that
// answers a fire-and-forget releaseStream/FCPublish (transaction id 0) is not a reason to
// lose the stream.
func TestStreamHandlerIgnoresAResponseToAnUnknownTransaction(t *testing.T) {
	for _, name := range []string{"_result", "_error"} {
		t.Run(name, func(t *testing.T) {
			c := newConn(&rwcMock{}, nil)
			s := newStream(42, c)

			err := s.handler.handleCommand(3, 0, &message.CommandMessage{
				CommandName:   name,
				TransactionID: 0,
				Encoding:      message.EncodingTypeAMF0,
				Body:          bytes.NewReader(nil),
			})
			require.Nil(t, err)
		})
	}
}
