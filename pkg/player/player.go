package player

import (
	"bytes"
	"strings"
	"text/template"
)

// GenericPlayer represents most players. The stream URL will be appended to the arguments.
type GenericPlayer struct {
	Name string
	Args []string

	GetHelperScriptPath func() string
}

// Player opens a stream URL in a video player.
type Player interface {
	Open(url string) error
}

type MediaEntry struct {
	URL string
	// URLs holds every entry of a playlist, URL is always its first item
	URLs []string
}

// NewPlaylist creates a MediaEntry that plays the given urls in order
func NewPlaylist(urls []string) MediaEntry {
	if len(urls) == 0 {
		return MediaEntry{}
	}

	return MediaEntry{URL: urls[0], URLs: urls}
}

func (p *GenericPlayer) expandArgs(data MediaEntry) ([]string) {
	var res []string
	var buffer bytes.Buffer

	for _, u := range p.Args {
		// a bare url placeholder expands to every playlist entry so players like mpv queue them
		if strings.TrimSpace(u) == "{{.URL}}" && len(data.URLs) > 1 {
			res = append(res, data.URLs...)
			continue
		}

		temp := template.New("")
		temp.Funcs(template.FuncMap{"GetHelperScriptPath": p.GetHelperScriptPath})
		temp, _ = temp.Parse(u)

		temp.Execute(&buffer, data)

		res = append(res, buffer.String())
		buffer.Reset()
	}

	return res
}
