package conversations

import (
	"crypto/sha256"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

type participant struct {
	digest      [sha256.Size]byte
	lastSeen    time.Time
	lostAt      time.Time
	hasJoined   bool
	joined      bool
	connected   bool
	publication api.RoomPublication
	signal      *api.RoomSignal
}

type room struct {
	id                    string
	expiresAt             time.Time
	inviteDigest          [sha256.Size]byte
	generation            api.RoomGeneration
	host                  *participant
	hostTracking          bool
	browserHandoffUsed    bool
	browserCapturePending bool
	guest                 *participant
	terminal              api.RoomState
	relay                 *RoomRelay
}

// Room transactions run under Service.mu. The memory store keeps only live
// rooms and a bounded set of terminal generations for repeat requests.
type roomRepository struct {
	rooms map[string]*room
}

func (r *roomRepository) get(id string) *room { return r.rooms[id] }
func (r *roomRepository) put(value *room)     { r.rooms[value.id] = value }
func (r *roomRepository) remove(id string)    { delete(r.rooms, id) }
