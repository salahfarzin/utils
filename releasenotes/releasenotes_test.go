package releasenotes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const emptyUnreleased = "# Unreleased\n\nChanges merged into `dev` since v1.10.0.\n"

func changelog(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

func TestRead(t *testing.T) {
	t.Run("the unreleased changes and the newest release, by version not name", func(t *testing.T) {
		notes, err := Read(changelog(map[string]string{
			"unreleased.md": emptyUnreleased + "\n## Features\n\n- Post views\n",
			"v1.9.2.md":     "# v1.9.2\n\nold",
			"v1.10.0.md":    "# v1.10.0\n\n## Features\n\n- Social publishing\n",
			"README.md":     "# Changelog",
		}), "auth")

		require.NoError(t, err)
		assert.Equal(t, "auth", notes.Service)
		assert.Contains(t, notes.Unreleased, "- Post views")
		require.NotNil(t, notes.Latest)
		assert.Equal(t, "v1.10.0", notes.Latest.Version)
		assert.Equal(t, "# v1.10.0\n\n## Features\n\n- Social publishing", notes.Latest.Notes)
	})

	t.Run("nothing unreleased just after tagging", func(t *testing.T) {
		notes, err := Read(changelog(map[string]string{"unreleased.md": emptyUnreleased, "v1.0.0.md": "# v1.0.0"}), "meet")

		require.NoError(t, err)
		assert.Empty(t, notes.Unreleased)
		assert.Equal(t, "v1.0.0", notes.Latest.Version)
	})

	t.Run("no release yet", func(t *testing.T) {
		notes, err := Read(changelog(map[string]string{"unreleased.md": "# Unreleased\n\n## Features\n\n- First"}), "forms")

		require.NoError(t, err)
		assert.Nil(t, notes.Latest)
		assert.NotEmpty(t, notes.Unreleased)
	})
}

func TestHandler(t *testing.T) {
	handler := Handler(changelog(map[string]string{"v2.0.0.md": "# v2.0.0\n\n## Features\n\n- New"}), "notification")

	t.Run("serves the notes as JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		var notes Notes
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &notes))
		assert.Equal(t, Notes{Service: "notification", Latest: &Release{Version: "v2.0.0", Notes: "# v2.0.0\n\n## Features\n\n- New"}}, notes)
	})

	t.Run("only reads", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, Path, nil))

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}
