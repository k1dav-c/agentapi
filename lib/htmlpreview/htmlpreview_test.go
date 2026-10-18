package htmlpreview_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/htmlpreview"
)

func dataURL(mediaType, content string) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString([]byte(content))
}

func inline(t *testing.T, fsys fstest.MapFS, name string) htmlpreview.Page {
	t.Helper()
	page, err := htmlpreview.Inline(fsys, name, fsys[name].Data)
	require.NoError(t, err)
	return page
}

func TestInlinesWhatThePageRefersTo(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"site/index.html": {Data: []byte(`<!DOCTYPE html>
<html><head>
<link rel="stylesheet" href="css/style.css" media="screen">
<link rel="icon" href="favicon.ico">
<script src="js/app.js" defer></script>
<style>body { background: url('img/bg.png'); }</style>
</head><body>
<img src="img/logo.png" srcset="img/logo.png 1x, img/logo@2x.png 2x" alt="logo">
<div style="background-image: url(img/bg.png)">x</div>
<video poster="img/bg.png" src="media/clip.mp4"></video>
<img src="/shared/pic%20one.png">
</body></html>`)},
		"site/css/style.css":    {Data: []byte(`@import "base.css"; @font-face { src: url("../fonts/a.woff2"); }`)},
		"site/css/base.css":     {Data: []byte(`h1 { background: url(../img/bg.png) }`)},
		"site/fonts/a.woff2":    {Data: []byte("font")},
		"site/favicon.ico":      {Data: []byte("ico")},
		"site/js/app.js":        {Data: []byte(`document.title = "</script>";`)},
		"site/img/bg.png":       {Data: []byte("bg")},
		"site/img/logo.png":     {Data: []byte("logo")},
		"site/img/logo@2x.png":  {Data: []byte("logo2x")},
		"site/media/clip.mp4":   {Data: []byte("clip")},
		"shared/pic one.png":    {Data: []byte("pic")},
		"site/unrelated.secret": {Data: []byte("secret")},
	}
	page := inline(t, fsys, "site/index.html")
	assert.Empty(t, page.Missing)

	bg := dataURL("image/png", "bg")
	base := dataURL("text/css;charset=utf-8", `h1 { background: url("`+bg+`") }`)
	style := dataURL("text/css;charset=utf-8", `@import url("`+base+`"); @font-face { src: url("`+dataURL("font/woff2", "font")+`"); }`)
	assert.Contains(t, page.HTML, `<link rel="stylesheet" href="`+style+`" media="screen">`)
	assert.Contains(t, page.HTML, `<link rel="icon" href="`+dataURL("image/x-icon", "ico")+`">`)
	// A script keeps what makes it deferred, or a module: it stays
	// external, with a data: URL.
	assert.Contains(t, page.HTML, `<script src="`+dataURL("text/javascript;charset=utf-8", `document.title = "</script>";`)+`" defer="">`)
	assert.Contains(t, page.HTML, `<style>body { background: url("`+bg+`"); }</style>`)
	logo := dataURL("image/png", "logo")
	assert.Contains(t, page.HTML, `<img src="`+logo+`" srcset="`+logo+` 1x, `+dataURL("image/png", "logo2x")+` 2x" alt="logo">`)
	assert.Contains(t, page.HTML, `<div style="background-image: url(&#34;`+bg+`&#34;)">`)
	assert.Contains(t, page.HTML, `<video poster="`+bg+`" src="`+dataURL("video/mp4", "clip")+`">`)
	// A path starting with "/" is from the working directory.
	assert.Contains(t, page.HTML, `<img src="`+dataURL("image/png", "pic")+`">`)
	assert.NotContains(t, page.HTML, base64.StdEncoding.EncodeToString([]byte("secret")))
}

func TestLeavesURLsAlone(t *testing.T) {
	t.Parallel()
	html := `<!doctype html><link rel="stylesheet" href="https://cdn.example.com/a.css">` +
		`<script src="//cdn.example.com/a.js"></script><img src="data:image/png;base64,AAAA">` +
		`<a href="#top">top</a><svg><rect style="fill: url(#gradient)"/></svg><div   class=x>kept</div>`
	fsys := fstest.MapFS{"index.html": {Data: []byte(html)}}
	page := inline(t, fsys, "index.html")
	assert.Empty(t, page.Missing)
	assert.Equal(t, html, strings.Replace(page.HTML, storageShim(page.HTML), "", 1), "nothing but the shim changes")
}

func TestListsWhatItLeavesOut(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"app/index.html": {Data: []byte(`<!doctype html>
<script src="missing.js"></script>
<script src="../../outside.js"></script>
<script src=".env.js"></script>
<script src="notes.txt"></script>
<link rel="stylesheet" href="app.js">
<img src="big.png">
<img src="dir.png">`)},
		"app/app.js":         {Data: []byte("1")},
		"app/.env.js":        {Data: []byte("SECRET=1")},
		"app/notes.txt":      {Data: []byte("notes")},
		"app/big.png":        {Data: []byte(strings.Repeat("x", htmlpreview.MaxAsset+1))},
		"app/dir.png/inside": {Data: []byte("x")},
	}
	page := inline(t, fsys, "app/index.html")
	assert.Equal(t, []htmlpreview.Missing{
		{Ref: "missing.js", Reason: "not found"},
		{Ref: "../../outside.js", Reason: "outside the working directory"},
		{Ref: ".env.js", Reason: "hidden"},
		{Ref: "notes.txt", Reason: "not a script"},
		{Ref: "app.js", Reason: "not a stylesheet"},
		{Ref: "big.png", Reason: "larger than 4 MiB"},
		{Ref: "dir.png", Reason: "not a file"},
	}, page.Missing)
	assert.Contains(t, page.HTML, `<script src="missing.js"></script>`)
	assert.NotContains(t, page.HTML, base64.StdEncoding.EncodeToString([]byte("SECRET=1")))
}

func TestStopsAtItsBudget(t *testing.T) {
	t.Parallel()
	image := strings.Repeat("x", 3<<20)
	var html strings.Builder
	html.WriteString("<!doctype html>")
	for range 8 {
		html.WriteString(`<img src="photo.png">`)
	}
	fsys := fstest.MapFS{
		"index.html": {Data: []byte(html.String())},
		"photo.png":  {Data: []byte(image)},
	}
	page := inline(t, fsys, "index.html")
	// Each copy of the 4 MiB data: URL counts, so three fit in 16 MiB.
	assert.Equal(t, 3, strings.Count(page.HTML, dataURL("image/png", image)))
	assert.Equal(t, []htmlpreview.Missing{{Ref: "photo.png", Reason: "the page's 16 MiB of inlined files are used up"}}, page.Missing)
	assert.LessOrEqual(t, len(page.HTML), htmlpreview.MaxTotal+len(html.String())+len(storageShim(page.HTML)))
}

func TestImportCycle(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"index.html": {Data: []byte(`<link rel="stylesheet" href="a.css">`)},
		"a.css":      {Data: []byte(`@import "b.css"; a { color: red }`)},
		"b.css":      {Data: []byte(`@import "a.css"; b { color: blue }`)},
	}
	page := inline(t, fsys, "index.html")
	assert.Equal(t, []htmlpreview.Missing{{Ref: "a.css", Reason: "imported too deep, or imported by itself"}}, page.Missing)
	b := dataURL("text/css;charset=utf-8", `@import "a.css"; b { color: blue }`)
	a := dataURL("text/css;charset=utf-8", `@import url("`+b+`"); a { color: red }`)
	assert.Contains(t, page.HTML, `href="`+a+`"`)
}

func TestStorageShimComesFirst(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ page, before string }{
		"after the head tag":   {`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><script>localStorage.x = 1</script></head></html>`, `<!DOCTYPE html><html lang="en"><head>`},
		"without a head tag":   {`<!doctype html><title>x</title>`, `<!doctype html>`},
		"before body content":  {`<p>hello</p>`, ``},
		"after leading text":   {"\n<!-- note -->\n<div>x</div>", "\n<!-- note -->\n"},
		"once with many items": {`<p>a</p><p>b</p>`, ``},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			page := inline(t, fstest.MapFS{"index.html": {Data: []byte(tc.page)}}, "index.html")
			shim := storageShim(page.HTML)
			require.NotEmpty(t, shim)
			assert.True(t, strings.HasPrefix(page.HTML, tc.before+shim), page.HTML)
			assert.Equal(t, 1, strings.Count(page.HTML, shim))
			assert.Equal(t, tc.page, strings.Replace(page.HTML, shim, "", 1))
		})
	}
}

func TestNoElementsNoShim(t *testing.T) {
	t.Parallel()
	page := inline(t, fstest.MapFS{"index.html": {Data: []byte("just text")}}, "index.html")
	assert.Equal(t, "just text", page.HTML)
}

// storageShim returns the shim Inline put into html.
func storageShim(html string) string {
	start := strings.Index(html, "<script>(()=>{const m=")
	if start < 0 {
		return ""
	}
	end := strings.Index(html[start:], "</script>")
	return html[start : start+end+len("</script>")]
}
