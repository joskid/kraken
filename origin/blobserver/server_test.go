package blobserver

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/docker/distribution/uuid"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"code.uber.internal/infra/kraken/lib/dockerregistry/image"
	"code.uber.internal/infra/kraken/lib/store"
	"code.uber.internal/infra/kraken/utils/httputil"
	"code.uber.internal/infra/kraken/utils/randutil"
)

func TestCheckBlobHandlerOK(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetCacheFileStat(d.Hex()).Return(nil, nil)

	ok, err := NewHTTPClient(clientConfigFixture(), addr).CheckBlob(d)
	require.NoError(err)
	require.True(ok)
}

func TestCheckBlobHandlerNotFound(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetCacheFileStat(d.Hex()).Return(nil, os.ErrNotExist)

	ok, err := NewHTTPClient(clientConfigFixture(), addr).CheckBlob(d)
	require.NoError(err)
	require.False(ok)
}

func TestGetBlobHandlerOK(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	d := image.DigestFixture()

	blob := randutil.Text(256)
	f, cleanup := store.NewMockFileReadWriter(blob)
	defer cleanup()

	mocks.fileStore.EXPECT().GetCacheFileReader(d.Hex()).Return(f, nil)

	r, err := NewHTTPClient(clientConfigFixture(), addr).GetBlob(d)
	require.NoError(err)
	b, err := ioutil.ReadAll(r)
	require.NoError(err)
	require.Equal(string(blob), string(b))
}

func TestGetBlobHandlerNotFound(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetCacheFileReader(d.Hex()).Return(nil, os.ErrNotExist)

	_, err := NewHTTPClient(clientConfigFixture(), addr).GetBlob(d)
	require.Error(err)
	require.Equal(http.StatusNotFound, err.(httputil.StatusError).Status)
}

func TestDeleteBlobHandlerAccepted(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().MoveCacheFileToTrash(d.Hex()).Return(nil)

	require.NoError(NewHTTPClient(clientConfigFixture(), addr).DeleteBlob(d))
}

func TestDeleteBlobHandlerNotFound(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().MoveCacheFileToTrash(d.Hex()).Return(os.ErrNotExist)

	err := NewHTTPClient(clientConfigFixture(), addr).DeleteBlob(d)
	require.Error(err)
	require.Equal(http.StatusNotFound, err.(httputil.StatusError).Status)
}

func TestStartUploadHandlerAccepted(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetCacheFileStat(d.Hex()).Return(nil, os.ErrNotExist)
	mocks.fileStore.EXPECT().CreateUploadFile(gomock.Any(), int64(0)).Return(nil)

	uuid, err := NewHTTPClient(clientConfigFixture(), addr).StartUpload(d)
	require.NoError(err)
	require.NotEmpty(uuid)
}

func TestStartUploadHandlerConflict(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetCacheFileStat(d.Hex()).Return(nil, nil)

	_, err := NewHTTPClient(clientConfigFixture(), addr).StartUpload(d)
	require.Error(err)
	require.Equal(http.StatusConflict, err.(httputil.StatusError).Status)
}

func TestPatchUploadHandlerAccepted(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, serverStop := mocks.server(configNoRedirectFixture())
	defer serverStop()

	u := uuid.Generate().String()
	d := image.DigestFixture()

	blob := randutil.Text(256)
	f, cleanup := store.NewMockFileReadWriter(blob)
	defer cleanup()

	chunk := randutil.Text(64)
	start := 128
	stop := start + len(chunk)

	mocks.fileStore.EXPECT().GetCacheFileStat(d.Hex()).Return(nil, os.ErrNotExist)
	mocks.fileStore.EXPECT().GetUploadFileReadWriter(u).Return(f, nil)

	require.NoError(
		NewHTTPClient(clientConfigFixture(), addr).PatchUpload(d, u, int64(start), int64(stop), bytes.NewBuffer(chunk)))

	content, err := ioutil.ReadFile(f.Name())
	require.NoError(err)
	require.Equal(
		string(blob[:start])+string(chunk)+string(blob[stop:]), string(content))
}

func TestPatchUploadHandlerConflict(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, serverStop := mocks.server(configNoRedirectFixture())
	defer serverStop()

	u := uuid.Generate().String()
	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetCacheFileStat(d.Hex()).Return(nil, nil)

	err := NewHTTPClient(clientConfigFixture(), addr).PatchUpload(d, u, 0, 4, bytes.NewBufferString("blah"))
	require.Error(err)
	require.Equal(http.StatusConflict, err.(httputil.StatusError).Status)
}

func TestCommitUploadHandlerCreated(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	u := uuid.Generate().String()

	blob := randutil.Text(256)
	f, cleanup := store.NewMockFileReadWriter(blob)
	defer cleanup()

	d, err := image.NewDigester().FromBytes(blob)
	require.NoError(err)

	mocks.fileStore.EXPECT().GetUploadFileReader(u).Return(f, nil)
	mocks.fileStore.EXPECT().MoveUploadFileToCache(u, d.Hex()).Return(nil)

	require.NoError(NewHTTPClient(clientConfigFixture(), addr).CommitUpload(d, u))
}

func TestCommitUploadHandlerNotFound(t *testing.T) {
	require := require.New(t)

	mocks := newServerMocks(t)
	defer mocks.ctrl.Finish()

	addr, stop := mocks.server(configNoRedirectFixture())
	defer stop()

	u := uuid.Generate().String()
	d := image.DigestFixture()

	mocks.fileStore.EXPECT().GetUploadFileReader(u).Return(nil, os.ErrNotExist)

	err := NewHTTPClient(clientConfigFixture(), addr).CommitUpload(d, u)
	require.Error(err)
	require.Equal(http.StatusNotFound, err.(httputil.StatusError).Status)
}

func TestParseContentRangeHeaderBadRequests(t *testing.T) {
	tests := []struct {
		description string
		value       string
	}{
		{"empty value", ""},
		{"invalid format", "blah"},
		{"invalid start", "blah-5"},
		{"invalid end", "5-blah"},
	}
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			require := require.New(t)

			h := http.Header{}
			h.Add("Content-Range", test.value)
			start, end, err := parseContentRange(h)
			require.Error(err)
			require.Equal(http.StatusBadRequest, err.(*serverError).status)
			require.Equal(int64(0), start)
			require.Equal(int64(0), end)
		})
	}
}

func TestRedirectErrors(t *testing.T) {
	d := image.DigestFixture()
	u := uuid.Generate().String()
	cc := clientConfigFixture()

	tests := []struct {
		name string
		f    func(addr string) error
	}{
		{
			"CheckBlob",
			func(addr string) error { _, err := NewHTTPClient(cc, addr).CheckBlob(d); return err },
		}, {
			"GetBlob",
			func(addr string) error { _, err := NewHTTPClient(cc, addr).GetBlob(d); return err },
		}, {
			"StartUpload",
			func(addr string) error { _, err := NewHTTPClient(cc, addr).StartUpload(d); return err },
		}, {
			"PatchUpload",
			func(addr string) error {
				return NewHTTPClient(cc, addr).PatchUpload(d, u, 0, 4, bytes.NewBufferString("blah"))
			},
		}, {
			"CommitUpload",
			func(addr string) error { return NewHTTPClient(cc, addr).CommitUpload(d, u) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			mocks := newServerMocks(t)
			defer mocks.ctrl.Finish()

			// Set the master we test against to have a weight of 0, such that all
			// requests will redirect to the other nodes.
			config := configFixture()
			node := config.HashNodes[master1]
			node.Weight = 0
			config.HashNodes[master1] = node

			addr, stop := mocks.server(config)
			defer stop()

			err := test.f(addr)
			require.Error(err)
			require.Equal([]string{"origin2", "origin3"}, err.(RedirectError).Locations)
		})
	}
}