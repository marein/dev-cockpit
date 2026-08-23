package voice

import "github.com/abadojack/whatlanggo"

// detectOptions restricts the detector to the languages the speech voices
// exist for; everything else only distorts the call between the two.
var detectOptions = whatlanggo.Options{
	Whitelist: map[whatlanggo.Lang]bool{
		whatlanggo.Deu: true,
		whatlanggo.Eng: true,
	},
}

// DetectLanguage answers the lowercase two letter code of the language the
// text reads as, out of the languages the speech voices exist for. Mixed text
// answers the dominant one, which is the voice that reads most of it right,
// and text the detector cannot place falls back to English, the same fallback
// the engine's voice map has. The detector is trigram based and deliberately
// small: lingua-go would be the more accurate pick, but it embeds every
// language model into the binary and more than tripled it, and telling German
// from English does not need that.
func DetectLanguage(text string) string {
	if whatlanggo.DetectWithOptions(text, detectOptions).Lang == whatlanggo.Deu {
		return "de"
	}
	return "en"
}
