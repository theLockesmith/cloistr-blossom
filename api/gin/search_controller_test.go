package gin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestContextWithQuery(rawQuery string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/api/blobs/search?"+rawQuery, nil)
	return c
}

func TestParseSearchFilter(t *testing.T) {
	t.Run("defaults apply a bounded limit", func(t *testing.T) {
		f := parseSearchFilter(newTestContextWithQuery(""))
		if f.Limit != 50 {
			t.Fatalf("default limit = %d, want 50", f.Limit)
		}
		if f.TypePrefix != "" || f.Pubkey != "" || f.SortDesc {
			t.Fatalf("expected empty filter, got %+v", f)
		}
	})

	t.Run("limit is capped at searchMaxLimit", func(t *testing.T) {
		f := parseSearchFilter(newTestContextWithQuery("limit=99999"))
		if f.Limit != searchMaxLimit {
			t.Fatalf("limit = %d, want %d", f.Limit, searchMaxLimit)
		}
	})

	t.Run("all parameters parse", func(t *testing.T) {
		f := parseSearchFilter(newTestContextWithQuery(
			"type=image/&pubkey=abc&since=100&until=200&min_size=10&max_size=20&offset=5&sort=desc&limit=25"))
		if f.TypePrefix != "image/" || f.Pubkey != "abc" {
			t.Fatalf("type/pubkey mismatch: %+v", f)
		}
		if f.Since != 100 || f.Until != 200 || f.MinSize != 10 || f.MaxSize != 20 {
			t.Fatalf("numeric filters mismatch: %+v", f)
		}
		if f.Offset != 5 || f.Limit != 25 || !f.SortDesc {
			t.Fatalf("pagination/sort mismatch: %+v", f)
		}
	})

	t.Run("garbage numerics are ignored", func(t *testing.T) {
		f := parseSearchFilter(newTestContextWithQuery("since=abc&min_size=-5&limit=xyz"))
		if f.Since != 0 || f.MinSize != 0 {
			t.Fatalf("expected zero values for bad numerics, got %+v", f)
		}
		if f.Limit != 50 {
			t.Fatalf("expected default limit on bad input, got %d", f.Limit)
		}
	})
}
