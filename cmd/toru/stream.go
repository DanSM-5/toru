package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/DanSM-5/toru/pkg/libtorrent"
	"github.com/DanSM-5/toru/pkg/player"
	"github.com/DanSM-5/toru/pkg/search"
	"github.com/anacrolix/torrent"
	"github.com/pterm/pterm"
)

// truncate a string to a specific length
func TruncateString(s string, length int) string {
	var l int
	var sb strings.Builder

	// early return if string is shorter then requested length
	if length >= len(s) {
		return s
	}

	for _, r := range s {
		if l <= length {
			sb.WriteRune(r)
		} else {

		}
		l++
	}
	return sb.String()
}

// print the progress of a specific torrent until context cancel is triggered
func Progress(t *torrent.Torrent, ctx context.Context) {
	title := TruncateString(t.Name(), 33)
	fmt.Println(title)

	if t == nil {
		log.Fatal("torrent is nil")
	}

	p, err := pterm.DefaultProgressbar.WithTotal(100).Start()
	if err != nil {
		log.Println(err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			p.Stop()
			return
		default:
			pc := float64(t.BytesCompleted()) / float64(t.Length()) * 100
			numpeers := len(t.PeerConns())
			p.Increment().Current = int(pc)
			p.UpdateTitle(fmt.Sprintf("peers [%v]", numpeers))
			time.Sleep(time.Millisecond * 50)
		}
	}
}

// takes any type of torrent file/url/magnet, adds it to the client and streams it
// torfile is any of magnet, link or path to .torrent file.
func StreamTorrent(torfile string, cl *libtorrent.Client) (player.MediaEntry, *torrent.Torrent, error) {
	success, _ := pterm.DefaultSpinner.Start("getting torrent info")
	t, err := cl.AddTorrent(torfile)
	if err != nil {
		return player.MediaEntry{}, nil, err
	}
	success.Success("Success!")

	files := t.Files()
	filesCount := len(files)

	if filesCount != 1 {
		fpaths, err := fzfEpisodes(files)
		if err != nil {
			// nothing will be streamed from it
			t.Drop()
			return player.MediaEntry{}, nil, err
		}

		var links []string
		for _, fpath := range fpaths {
			links = append(links, cl.ServeTorrentEpisode(t, fpath))
		}

		return player.NewPlaylist(links), t, nil
	}

	file := files[0].DisplayPath()
	link := cl.ServeTorrentEpisode(t, file)

	return player.MediaEntry{URL: link}, t, nil
}

// play a single torrent from a provided magnet, torrent or torrent URL
// returns the played media so it can be replayed later
func PlayTorrent(cl *libtorrent.Client, magnet string) (player.MediaEntry, *torrent.Torrent, error) {
	link, t, err := StreamTorrent(magnet, cl)
	if err != nil {
		return player.MediaEntry{}, nil, err
	}

	return link, t, PlayMedia(link, t)
}

// open the media in the player and wait until the player exits
func PlayMedia(link player.MediaEntry, t *torrent.Torrent) error {
	p, err := player.NewPlayer(options.Player)
	if err != nil {
		return err
	}

	// get a new player and start the media
	proc, err := p.Open(link)
	if err != nil {
		return err
	}

	// run progress in the background and cancel the progress bar when the player exits
	ctx, cancel := context.WithCancel(context.Background())
	go Progress(t, ctx)

	var px *os.ProcessState

	// wait for player to close
	px, err = proc.Wait()

	for !px.Exited() {
		time.Sleep(time.Millisecond * 500)
	}

	cancel()

	return nil
}

// play a torrent given on the command line, quitting when no episode is selected
func playArg(cl *libtorrent.Client, torfile string) error {
	_, _, err := PlayTorrent(cl, torfile)
	if errors.Is(err, errAbort) {
		os.Exit(0)
	}

	return err
}

// stream subcommand entry point
func runStream(cl *libtorrent.Client) error {
	if streaming.Magnet != "" {
		if err := playArg(cl, streaming.Magnet); err != nil {
			return err
		}
	}

	if streaming.TorrentFile != "" {
		if err := playArg(cl, streaming.TorrentFile); err != nil {
			return err
		}
	}

	if streaming.Args.Query != "" {
		if err := playArg(cl, streaming.Args.Query); err != nil {
			return err
		}
	}

	if streaming.Latest {
		res, err := search.LatestAnime(streaming.Args.Query, options.Proxy, 1)
		if err != nil {
			return err
		}

		if err := SelectAndPlay(cl, res); err != nil {
			return err
		}
	}

	return nil
}
