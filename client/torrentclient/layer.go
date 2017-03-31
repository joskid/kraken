package torrentclient

import (
	"path/filepath"

	"os"

	"fmt"

	"code.uber.internal/go-common.git/x/log"
	"code.uber.internal/infra/kraken/configuration"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

const (
	statusSuffix = "-status"
	timeout      = 120 //sec
)

// GetLayerKey returns the layer key given file name
func GetLayerKey(fname string) string {
	return filepath.Base(fname)
}

// LayerStore contains layer info and a pointer to cache to retrieve data
type LayerStore struct {
	m      *Manager
	config *configuration.Config
	name   string
	pieces []*PieceStore
}

// NewLayerStore returns a new LayerStore. Caller should then call either LoadFromDisk or CreateEmptyLayerFile.
func NewLayerStore(m *Manager, name string) *LayerStore {
	return &LayerStore{
		name:   name,
		m:      m,
		config: m.config,
	}
}

func (ls *LayerStore) numPieces() int {
	return len(ls.pieces)
}

func (ls *LayerStore) pieceStatusPath() string {
	return ls.downloadPath() + statusSuffix
}

func (ls *LayerStore) cachePath() string {
	return ls.config.CacheDir + filepath.Base(ls.name)
}

func (ls *LayerStore) downloadPath() string {
	return ls.config.DownloadDir + filepath.Base(ls.name)
}

func (ls *LayerStore) loadPieces(n int) {
	ls.pieces = make([]*PieceStore, n)
	for i := 0; i < n; i++ {
		ls.pieces[i] = NewPieceStore(ls, i, done)
	}
}

func (ls *LayerStore) initPieces(n int) error {
	status := make([]byte, n)
	ls.pieces = make([]*PieceStore, n)
	for i := 0; i < n; i++ {
		ls.pieces[i] = NewPieceStore(ls, i, clean)
		status[i] = clean
	}
	// write clean statuses for all pieces
	f, err := os.Create(ls.pieceStatusPath())
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(status)
	if err != nil {
		return err
	}
	return nil
}

func (ls *LayerStore) tryCacheLayer() error {
	f, err := os.Open(ls.pieceStatusPath())
	if err != nil {
		return err
	}

	bu := make([]byte, len(ls.pieces))
	_, err = f.Read(bu)
	if err != nil {
		f.Close()
		return err
	}

	log.Debugf("%s", bu)

	for _, b := range bu {
		if b != done {
			f.Close()
			return fmt.Errorf("Download is not completed yet. Unable to cache layer file %s", ls.name)
		}
	}

	f.Close()
	// try cache layer
	_, ok, _ := ls.m.lru.Add(GetLayerKey(ls.name), ls.cachePath(), func(string) error {
		err = os.Rename(ls.downloadPath(), ls.cachePath())
		if err != nil {
			log.Error(err.Error())
			return err
		}
		os.Remove(ls.pieceStatusPath())
		return nil
	})
	if !ok {
		return fmt.Errorf("Failed to cache layer file %s", ls.name)
	}

	return nil
}

// TryCacheLayer checks if all pieces are marked as done and try to add layer to the cache
func (ls *LayerStore) TryCacheLayer() error {
	ls.m.mu.Lock()
	defer ls.m.mu.Unlock()
	return ls.tryCacheLayer()
}

// LoadFromDisk loads data and piece info from disk. called once at restart
func (ls *LayerStore) LoadFromDisk() error {
	fi, err := os.Stat(ls.pieceStatusPath())
	if err != nil {
		return err
	}

	numPieces := int(fi.Size())

	status := make([]uint8, numPieces)
	pieces := make([]*PieceStore, numPieces)

	f, err := os.Open(ls.pieceStatusPath())
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Read(status)
	if err != nil {
		return err
	}

	for i := 0; i < numPieces; i++ {
		pieces[i] = NewPieceStore(ls, i, status[i])
	}

	ls.pieces = pieces
	return nil
}

// IsDownloading returns true if the layer is downloading
func (ls *LayerStore) IsDownloading() (bool, error) {
	_, err := os.Stat(ls.downloadPath())
	if err == nil {
		return true, nil
	}

	return false, err
}

// IsDownloaded returns true if layer is cached
func (ls *LayerStore) IsDownloaded() (string, bool) {
	return ls.m.lru.Get(GetLayerKey(ls.name), nil)
}

// CreateEmptyLayerFile creates a sparse data file for the torrent in download directory
func (ls *LayerStore) CreateEmptyLayerFile(len int64, numPieces int) error {
	// in case of DownloadDir does not exit
	err := os.MkdirAll(ls.config.DownloadDir, perm)
	if err != nil {
		log.WithFields(log.Fields{
			"name": ls.name,
			"dir":  ls.config.DownloadDir,
			"err":  err,
		}).Error("Error creating download directory")
		return err
	}

	// init piece
	err = ls.initPieces(numPieces)
	if err != nil {
		log.WithFields(log.Fields{
			"name":      ls.name,
			"numPieces": numPieces,
			"err":       err,
		}).Error("Error initiating pieces")
		return err
	}

	// get total size
	fp := ls.downloadPath()

	// create download file
	f, err := os.Create(fp)
	if err != nil {
		log.WithFields(log.Fields{
			"name": ls.name,
			"path": fp,
			"err":  err,
		}).Error("Error creating empty torrent file")
		return err
	}
	defer f.Close()

	// change size
	err = f.Truncate(len)
	if err != nil {
		log.WithFields(log.Fields{
			"name": ls.name,
			"path": fp,
			"size": len,
			"err":  err,
		}).Error("Error changing empty torrent file size")
		return err
	}

	log.Debugf("Successfully created empty layer file in download directory %s", fp)
	return nil
}

// Piece returns pieceStore of the layer given metainfo
func (ls *LayerStore) Piece(p metainfo.Piece) storage.PieceImpl {
	if p.Index() >= len(ls.pieces) {
		log.WithFields(log.Fields{
			"name":  ls.name,
			"piece": p.Index(),
		}).Error("Piece index out of range")
		return nil
	}
	piece := ls.pieces[p.Index()]
	if piece == nil {
		log.WithFields(log.Fields{
			"name":  ls.name,
			"piece": p.Index(),
		}).Error("Invalid piece info")
		return nil
	}

	return piece
}

// Close closes the LayerStore
func (ls *LayerStore) Close() error {
	ls.m.mu.Lock()
	defer ls.m.mu.Unlock()

	// try cache
	ls.tryCacheLayer()

	// remove itself
	delete(ls.m.opened, ls.name)

	return nil
}