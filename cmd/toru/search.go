package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"sort"

	fzf "github.com/DanSM-5/fzf.lib/src"
	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/lipgloss"
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

// errAbort is returned by the pickers when the user quits without selecting anything
var errAbort = errors.New("abort")

// pick runs fzf over items and returns the indexes of the accepted items.
// preview renders the preview window for an item, nil disables it
func pick(items []string, preview func(i int) string, args ...string) ([]int, error) {
	args = append([]string{"--layout=reverse"}, args...)
	if preview != nil {
		args = append(args, "--preview-window=right:50%:wrap-word")
	}

	opts, err := fzf.Parse(fzf.LibOptions{Args: args})
	if err != nil {
		return nil, err
	}

	input := make(chan string, len(items))
	for _, item := range items {
		input <- item
	}
	close(input)
	opts.Input = input

	// the selection is read from opts.Accepted instead of the printed lines
	opts.Printer = func(string) {}

	if preview != nil {
		opts.SetPreviewFunc(func(ctx context.Context, s *fzf.State, w io.Writer) error {
			if s.Current == nil {
				_, err := fmt.Fprintln(w, "No matches")
				return err
			}

			_, err := io.WriteString(w, preview(s.Current.Index))
			return err
		}, fzf.PreviewOnQuery)
	}

	code, err := fzf.Run(opts)
	switch code {
	case fzf.ExitOk:
	case fzf.ExitNoMatch, fzf.ExitInterrupt:
		return nil, errAbort
	default:
		if err == nil {
			err = fmt.Errorf("fzf exited with code %d", code)
		}
		return nil, err
	}

	var idxs []int
	for _, e := range opts.Accepted() {
		idxs = append(idxs, e.Index)
	}

	return idxs, nil
}

func fzfMain() (string, error) {
	m := []string{"replay", "select", "search", "exit"}
	idxs, err := pick(m, nil)

	// User has selected nothing
	if err != nil {
		return "exit", err
	}

	return m[idxs[0]], nil
}

// select one or more episodes (Tab to mark multiple), returned in playlist order
func fzfEpisodes(m []*torrent.File) ([]string, error) {
	var names []string
	for _, f := range m {
		names = append(names, f.DisplayPath())
	}

	fpaths, err := pickEpisodes(names)

	// User has selected nothing
	if err != nil {
		if errors.Is(err, errAbort) {
			os.Exit(0)
		} else {
			return nil, err
		}
	}

	return fpaths, nil
}

// select one or more of the given episode paths, returned sorted
func pickEpisodes(names []string) ([]string, error) {
	idxs, err := pick(names, nil, "--multi", "--header=Tab to select multiple episodes")
	if err != nil {
		return nil, err
	}

	var fpaths []string
	for _, idx := range idxs {
		fpaths = append(fpaths, names[idx])
	}
	sort.Strings(fpaths)

	return fpaths, nil
}

func fzfMenu(m []nyaa.Media) (nyaa.Media, error) {
	idxs, err := pick(mediaNames(m), func(i int) string {
		return FormatMedia(m[i])
	})

	// User has selected nothing
	if err != nil {
		if errors.Is(err, errAbort) {
			os.Exit(0)
		} else {
			return nyaa.Media{}, err
		}
	}

	return m[idxs[0]], nil
}

func fzfMenuMulti(m []nyaa.Media) ([]nyaa.Media, error) {
	idxs, err := pick(mediaNames(m), func(i int) string {
		return FormatMedia(m[i])
	}, "--multi")

	// User has selected nothing
	if err != nil {
		if errors.Is(err, errAbort) {
			os.Exit(0)
		} else {
			return nil, err
		}
	}

	var matches []nyaa.Media
	for _, idx := range idxs {
		matches = append(matches, m[idx])
	}

	return matches, nil
}

func mediaNames(m []nyaa.Media) []string {
	var names []string
	for _, media := range m {
		names = append(names, media.Name)
	}

	return names
}
