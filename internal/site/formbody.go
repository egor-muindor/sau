package site

import (
	"strconv"
	"strings"

	"sau/internal/secrets"
	"sau/internal/translation"
)

// jsEscapeUnreserved is the set encodeURIComponent leaves alone.
// Note ( ) ! * ' ~ : url.QueryEscape percent-encodes them, jQuery.param does not.
const jsEscapeUnreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"

const hexDigits = "0123456789ABCDEF"

// jsEscape encodes s the way jQuery.param does: encodeURIComponent, then a
// space becomes '+' instead of %20. Multibyte runes are percent-encoded per
// UTF-8 byte.
func jsEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(jsEscapeUnreserved, c) >= 0:
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

// encodePairs joins ordered key/value pairs into a form body. Order is part of
// the contract and one key may repeat, so url.Values must not be used here:
// it sorts keys and cannot express a repeated name.
func encodePairs(pairs [][2]string) []byte {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(jsEscape(p[0]))
		b.WriteByte('=')
		b.WriteString(jsEscape(p[1]))
	}
	return []byte(b.String())
}

// EncodeSubmitForm builds the body of the create-translation POST.
// The field order is taken verbatim from a captured browser submit
// (.work/captures/network.log line 455) and is verified byte for byte by
// TestEncodeSubmitFormMatchesCapture. Do not reorder, do not drop the two
// empty qqfile fields, and keep the channel in both channel fields.
func EncodeSubmitForm(csrf string, d translation.Draft, ch translation.Channel, videoField, subField string) []byte {
	addedByAuthor := "0"
	if d.AddedByAuthor {
		addedByAuthor = "1"
	}
	channel := ch.CookieValue()
	return encodePairs([][2]string{
		{"csrf", csrf},
		{"TranslationAdminForm[seriesIdNew]", strconv.Itoa(d.SeriesID)},
		{"TranslationAdminForm[episodeNumberNew]", d.EpisodeNumber},
		{"TranslationAdminForm[episodeTypeNew]", string(d.EpisodeType)},
		{"TranslationAdminForm[type]", string(d.Type)},
		{"TranslationAdminForm[authorsNew]", d.Authors},
		{"TranslationAdminForm[addedByAuthor]", addedByAuthor},
		{"upload-channel", channel},
		{"upload-channel-mobile", channel},
		{"TranslationAdminForm[videoFileNew]", videoField},
		{"qqfile", ""},
		{"TranslationAdminForm[subFileNew]", subField},
		{"qqfile", ""},
		{"yt0", ""},
		{"dynpage", "1"},
	})
}

// EncodeLoginForm builds the body of the login POST. The field order comes from
// a captured browser login (.work/captures/network-login.log line 9); there is
// no "remember me" field.
//
// This is the only call of Secret.Reveal() outside package secrets. Keep it so:
// the password never reaches config, state, the cookie file or the logs, and
// that is guaranteed by the type, not by caller discipline.
func EncodeLoginForm(csrf, user string, pass secrets.Secret) []byte {
	return encodePairs([][2]string{
		{"csrf", csrf},
		{"LoginForm[username]", user},
		{"LoginForm[password]", pass.Reveal()},
		{"yt0", ""},
		{"dynpage", "1"},
	})
}
