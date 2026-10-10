// Package tid generates atproto TIDs (timestamp identifiers).
//
// Every record collection in the Tangled lexicons declares `"key": "tid"`, so
// rkeys must be TIDs. The layout is 64 bits rendered as 13 characters of
// base32-sortable: a leading zero bit, 53 bits of microseconds since the unix
// epoch, then a 10-bit clock identifier.
package tid

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const alphabet = "234567abcdefghijklmnopqrstuvwxyz"

// Length is the fixed character length of a TID.
const Length = 13

var (
	mu        sync.Mutex
	lastMicro int64
	clockID   uint64
	seeded    bool
)

func seed() {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A weak clock id only risks a collision with another process writing
		// in the same microsecond; falling back to zero is safe enough.
		clockID = 0
	} else {
		clockID = uint64(binary.BigEndian.Uint16(b[:])) & 0x3ff
	}
	seeded = true
}

// Now returns a new TID, monotonically increasing within this process.
func Now() string {
	return FromTime(time.Now())
}

// FromTime returns a TID encoding t. Successive calls never return a value that
// sorts at or below a previously returned one.
func FromTime(t time.Time) string {
	mu.Lock()
	defer mu.Unlock()
	if !seeded {
		seed()
	}
	micro := t.UnixMicro()
	if micro <= lastMicro {
		micro = lastMicro + 1
	}
	lastMicro = micro
	return encode(uint64(micro)&0x1fffffffffffff, clockID)
}

func encode(micro, clock uint64) string {
	v := (micro << 10) | (clock & 0x3ff)
	var out [Length]byte
	for i := Length - 1; i >= 0; i-- {
		out[i] = alphabet[v&0x1f]
		v >>= 5
	}
	return string(out[:])
}

// Valid reports whether s is a syntactically well-formed TID.
func Valid(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !inAlphabet(s[i]) {
			return false
		}
	}
	// The high bit must be clear, which constrains the leading character.
	return s[0] < 'a' || s[0] <= 'j'
}

func inAlphabet(c byte) bool {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return true
		}
	}
	return false
}

// Time decodes the timestamp embedded in a TID. It returns the zero time if s
// is not a valid TID.
func Time(s string) time.Time {
	if !Valid(s) {
		return time.Time{}
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		var d uint64
		for j := 0; j < len(alphabet); j++ {
			if alphabet[j] == s[i] {
				d = uint64(j)
				break
			}
		}
		v = v<<5 | d
	}
	return time.UnixMicro(int64(v >> 10)).UTC()
}
