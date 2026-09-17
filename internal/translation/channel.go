package translation

import (
	"fmt"
	"strconv"
	"strings"
)

// Channel is the delivery channel of the upload servers. The site keeps it in
// the upload-channel cookie and repeats it in two form fields. The channel
// decides which upload hosts the form page offers, so it is part of the resume
// identity: the same server id can belong to different channels.
type Channel int

const (
	ChannelAll Channel = 0
	ChannelCDN Channel = 2
	ChannelRU  Channel = 3
)

// DefaultChannel is what the tool uses unless told otherwise.
const DefaultChannel = ChannelCDN

// ParseChannel converts a human spelling into a Channel. Case and surrounding
// spaces are ignored; numeric spellings are rejected on purpose so that the
// wire values never leak into the user interface.
func ParseChannel(s string) (Channel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all":
		return ChannelAll, nil
	case "cdn":
		return ChannelCDN, nil
	case "ru":
		return ChannelRU, nil
	}
	return 0, fmt.Errorf("translation: unknown channel %q, want all, cdn or ru", s)
}

// String returns the human spelling of the channel.
func (c Channel) String() string {
	switch c {
	case ChannelAll:
		return "all"
	case ChannelCDN:
		return "cdn"
	case ChannelRU:
		return "ru"
	}
	return "channel(" + strconv.Itoa(int(c)) + ")"
}

// CookieValue returns the value the site expects in the upload-channel cookie
// and in the upload-channel form fields.
func (c Channel) CookieValue() string {
	return strconv.Itoa(int(c))
}
