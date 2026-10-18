package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"sort"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/lipgloss"
	fzf "github.com/ktr0731/go-fuzzyfinder"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/sweetbbak/toru/pkg/libtorrent"
	"github.com/sweetbbak/toru/pkg/nyaa"
	"github.com/sweetbbak/toru/pkg/search"
)

func runSearch(cl *libtorrent.Client) error {
	s := search.NewSearch()
	// list category options and return
	if searchopts.List {
		search.List()
		return nil
	}

	// build the query
	if searchopts.Category != "" {
		s.Category = searchopts.Category
	}
	if searchopts.Filter != "" {
		s.Filter = searchopts.Filter
	}
	if searchopts.SortBy != "" {
		s.SortBy = searchopts.SortBy
	}
	if searchopts.SortOrder != "" {
		s.SortOrder = searchopts.SortOrder
	}
	if searchopts.User != "" {
		s.User = searchopts.User
	}
	if searchopts.Args.Query != "" {
		s.Args.Query = searchopts.Args.Query
	}
	if searchopts.Page != 0 {
		s.Page = searchopts.Page
	}

	if searchopts.Latest {
		// search.LatestAnime(searchopts.Args.Query, 1)
		s = &search.Search{
			SortBy:    "id",
			SortOrder: "desc",
			Page:      1,
			Category:  "subs",
		}
	}

	if options.Proxy != "" {
		s.ProxyURL = options.Proxy
	}

	// make the request for results to nyaa.si
	m, err := s.Query()
	if err != nil {
		return err
	}

	// print and/or handle results
	// TODO: change os.Stdout to a generic io.Writer and support a file directly
	if searchopts.Json {
		err := m.WriteToJson(os.Stdout)
		if err != nil {
			return err
		}
		return nil
	}

	if searchopts.Stdout {
		m.PrintResults()
	}

	if searchopts.Stream || searchopts.Interactive {
		return SelectAndPlay(cl, m)
	}

	if !searchopts.Stdout && !searchopts.Json && !searchopts.Stream {
		m.PrintResults()
	}

	return nil
}

// basic search and play
func InteractiveSearch(cl *libtorrent.Client, query string) error {
	header := cutePrint.Align(lipgloss.Center).Render("Toru, stream anime, no strings attached")
	fmt.Println(header)

	var s string
	var err error
	if query == "" {
		s, err = Prompt("Search for an anime: ")
		if err != nil {
			return err
		}
	} else {
		s = query
	}

	m, err := search.LatestAnime(s, options.Proxy, 1)
	if err != nil {
		return err
	}

	cj := path.Join(cl.DataDir, "cache.json")
	err = m.Cache(cj)
	if err != nil {
		log.Println(err)
	}

	return SelectAndPlay(cl, m)
}

func SelectAndPlay(cl *libtorrent.Client, res *search.Results) error {
LOOP:
	// select a result
	choice, err := fzfMenu(res.Media)
	if err != nil {
		return err
	}

	// play that result
	media, t, err := PlayTorrent(cl, choice.Magnet)
	if err != nil {
		return err
	}

	for {
		// replay, select, search or exit
		action, err := fzfMain()
		if err != nil {
			return err
		}

		switch action {
		case "replay":
			if err := PlayMedia(media, t); err != nil {
				return err
			}
			continue
		case "select":
			goto LOOP
		case "search":
			if err := InteractiveSearch(cl, ""); err != nil {
				log.Fatal(err)
			}
		case "exit":
			os.Exit(0)
		}

		return nil
	}
}

func fzfMain() (string, error) {
	m := []string{"replay", "select", "search", "exit"}
	idx, err := fzf.Find(
		m,
		func(i int) string {
			return m[i]
		},
	)

	// User has selected nothing
	if err != nil {
		if errors.Is(err, fzf.ErrAbort) {
			return "exit", err
		} else {
			return "exit", err
		}
	}

	return m[idx], nil
}

// select one or more episodes (Tab to mark multiple), returned in playlist order
func fzfEpisodes(m []*torrent.File) ([]string, error) {
	l := len(m) - 1
	idxs, err := fzf.FindMulti(
		m,
		func(i int) string {
			// fpath := m[i].DisplayPath()
			fpath := m[l-i].DisplayPath()
			return fpath
		},
		fzf.WithHeader("Tab to select multiple episodes"),
	)

	// User has selected nothing
	if err != nil {
		if errors.Is(err, fzf.ErrAbort) {
			os.Exit(0)
		} else {
			return nil, err
		}
	}

	var fpaths []string
	for _, idx := range idxs {
		fpaths = append(fpaths, m[l-idx].DisplayPath())
	}
	sort.Strings(fpaths)

	return fpaths, nil
}

func fzfMenu(m []nyaa.Media) (nyaa.Media, error) {
	idx, err := fzf.Find(
		m,
		func(i int) string {
			return m[i].Name
		},
		fzf.WithPreviewWindow(func(i, width, height int) string {
			if i == -1 {
				return "No matches"
			}

			return wrapPreview(FormatMedia(m[i]), width)

		}),
	)

	// User has selected nothing
	if err != nil {
		if errors.Is(err, fzf.ErrAbort) {
			os.Exit(0)
		} else {
			return nyaa.Media{}, err
		}
	}

	return m[idx], nil
}

func fzfMenuMulti(m []nyaa.Media) ([]nyaa.Media, error) {
	idxs, err := fzf.FindMulti(
		m,
		func(i int) string {
			return m[i].Name
		},
		fzf.WithPreviewWindow(func(i, width, height int) string {
			if i == -1 {
				return "No matches"
			}

			return wrapPreview(FormatMedia(m[i]), width)

		}),
	)

	var matches []nyaa.Media
	for _, item := range idxs {
		matches = append(matches, m[item])
	}

	// User has selected nothing
	if err != nil {
		if errors.Is(err, fzf.ErrAbort) {
			os.Exit(0)
		} else {
			return nil, err
		}
	}

	return matches, nil
}

// word wrap text to fit inside the preview window, which takes the right half of
// the terminal minus its borders and padding. long words without spaces are hard wrapped
func wrapPreview(s string, width int) string {
	w := width - width/2 - 4
	if w < 1 {
		return s
	}

	return wrap.String(wordwrap.String(s, w), w)
}
