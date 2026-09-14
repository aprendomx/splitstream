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

// TestStreamHandlerIgnoresAResponseToAnUnknownTransaction: on the CONTROL stream, a
// _result or _error for a transaction this side never registered is dropped with a debug
// line, not turned into an error. handleCommand's error reaches Conn.handleMessage, which
// is not prepared to swallow it, so returning one here tears the whole connection down —
// and a peer that answers a fire-and-forget releaseStream/FCPublish (transaction id 0,
// sent over stream 0) is not a reason to lose the stream.
func TestStreamHandlerIgnoresAResponseToAnUnknownTransaction(t *testing.T) {
	for _, name := range []string{"_result", "_error"} {
		t.Run(name, func(t *testing.T) {
			c := newConn(&rwcMock{}, nil)
			s := newStream(ControlStreamID, c)

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

// TestStreamHandlerFailsOnAnUnknownTransactionOverADataStream: the boundary of the test
// above. Over a DATA stream the same orphan response keeps returning an error, which is
// what tears the connection down and makes the caller reconnect.
//
// This is not a theoretical case: a platform that refuses a `publish` answers it with an
// `_error` that also carries transaction id 0 and matches no registered transaction. If
// the tolerance were not scoped to stream 0, that refusal would turn into a debug line
// and the publisher would sit on a connection that will never carry video.
func TestStreamHandlerFailsOnAnUnknownTransactionOverADataStream(t *testing.T) {
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
			require.NotNil(t, err)
		})
	}
}

// TestStreamHandlerDeliversAResponseToAKnownTransaction: the counterpart of the test
// above — a _result or _error for a transaction this side DID register (the same path
// Stream.Command/CreateStream use, via transactions.Create) must still reach the caller
// waiting on it. Ignoring unknown transactions must not turn into ignoring known ones
// too: handleCommand keeps resolving the transaction and returning nil, exactly as
// before this round's fix.
func TestStreamHandlerDeliversAResponseToAKnownTransaction(t *testing.T) {
	for _, name := range []string{"_result", "_error"} {
		t.Run(name, func(t *testing.T) {
			c := newConn(&rwcMock{}, nil)
			s := newStream(42, c)

			const transactionID = 7
			tr, err := s.transactions.Create(transactionID)
			require.Nil(t, err)

			err = s.handler.handleCommand(3, 0, &message.CommandMessage{
				CommandName:   name,
				TransactionID: transactionID,
				Encoding:      message.EncodingTypeAMF0,
				Body:          bytes.NewReader(nil),
			})
			require.Nil(t, err)

			// The transaction was resolved with the reply, not left hanging.
			select {
			case <-tr.doneCh:
			default:
				t.Fatal("transaction was not resolved")
			}
			require.Equal(t, name, tr.commandName)

			// And it's gone from the table, same as before this round's fix.
			_, err = s.transactions.At(transactionID)
			require.NotNil(t, err)
		})
	}
}
