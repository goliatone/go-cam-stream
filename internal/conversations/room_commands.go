package conversations

import "github.com/goliatone/go-cam-stream/data/api"

type CreateRoom struct{}

func (CreateRoom) Type() string    { return "conversations.create.v1" }
func (CreateRoom) Validate() error { return nil }

type RedeemRoom struct {
	api.RoomRef
	api.RoomRedeemRequest
}

func (RedeemRoom) Type() string { return "conversations.redeem.v1" }
func (m RedeemRoom) Validate() error {
	if err := m.RoomRef.Validate(); err != nil {
		return err
	}
	return m.RoomRedeemRequest.Validate()
}

type JoinRoom struct {
	api.RoomRef
	api.RoomJoinRequest
}

func (JoinRoom) Type() string { return "conversations.join.v1" }
func (m JoinRoom) Validate() error {
	if err := m.RoomRef.Validate(); err != nil {
		return err
	}
	return m.RoomJoinRequest.Validate()
}

type PublishRoomSignal struct {
	api.RoomRef
	api.RoomSignal
}

func (PublishRoomSignal) Type() string { return "conversations.signal.v1" }
func (m PublishRoomSignal) Validate() error {
	if err := m.RoomRef.Validate(); err != nil {
		return err
	}
	return m.RoomSignal.Validate()
}

type SetRoomPublication JoinRoom

func (SetRoomPublication) Type() string      { return "conversations.set_publication.v1" }
func (m SetRoomPublication) Validate() error { return JoinRoom(m).Validate() }

type LeaveRoom struct{ api.RoomRef }

func (LeaveRoom) Type() string      { return "conversations.leave.v1" }
func (m LeaveRoom) Validate() error { return m.RoomRef.Validate() }

type EndRoom struct{ api.RoomRef }

func (EndRoom) Type() string { return "conversations.end.v1" }

type SetRoomConnection struct {
	api.RoomRef
	api.RoomConnectionRequest
}

func (SetRoomConnection) Type() string { return "conversations.set_connection.v1" }
func (m SetRoomConnection) Validate() error {
	if err := m.RoomRef.Validate(); err != nil {
		return err
	}
	return m.RoomConnectionRequest.Validate()
}
