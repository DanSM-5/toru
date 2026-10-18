package libtorrent

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// newLocalClient returns a client that only talks to peers it is given
func newLocalClient(t *testing.T, dir string, seed bool) *torrent.Client {
	t.Helper()
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.DefaultStorage = storage.NewFileByInfoHash(dir)
	cfg.Seed = seed
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.DisableIPv6 = true
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

// The download recovers when writing to disk fails for a while, as when another
// process holds the file on Windows
func TestWriteErrorRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("transfers a torrent between local clients")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only files")
	}

	// a seeder with a 2 MiB file
	seedDir := t.TempDir()
	name := "Example Show - 01.mkv"
	data := make([]byte, 2<<20)
	rand.Read(data)
	if err := os.WriteFile(filepath.Join(seedDir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(filepath.Join(seedDir, name)); err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{}
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	seeder := newLocalClient(t, seedDir, true)
	seedTorrent, _ := seeder.AddTorrentOpt(torrent.AddTorrentOpts{
		InfoHash:  mi.HashInfoBytes(),
		InfoBytes: mi.InfoBytes,
		// the file is in seedDir itself, not in an info hash directory
		Storage: storage.NewFile(seedDir),
	})
	seedTorrent.VerifyData()

	// a leecher whose file can't be written to at first
	leechDir := t.TempDir()
	leecher := newLocalClient(t, leechDir, false)
	target := filepath.Join(leechDir, mi.HashInfoBytes().HexString(), name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(1200 * time.Millisecond)
		os.Chmod(target, 0o644)
	}()

	lt, err := leecher.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	retryWriteErrors(lt)
	<-lt.GotInfo()
	lt.DownloadAll()
	lt.AddClientPeer(seeder)

	deadline := time.After(30 * time.Second)
	for lt.BytesCompleted() != lt.Length() {
		select {
		case <-deadline:
			t.Fatalf("download stalled at %d/%d bytes", lt.BytesCompleted(), lt.Length())
		case <-time.After(50 * time.Millisecond):
		}
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Error("the downloaded file differs from the seeded one")
	}
}
