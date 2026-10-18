// Package htmlpreview turns an HTML file into a page that shows by itself:
// the stylesheets, scripts, images, fonts and media it refers to by
// relative path become data: URLs. The chat shows the page in a sandboxed
// iframe that has no origin of its own, whose requests to AgentAPI would
// carry neither the chat's session cookie nor the cookie of a proxy in
// front of it.
package htmlpreview

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	// MaxAsset is how large a file is inlined.
	MaxAsset = 4 << 20
	// MaxTotal bounds the data: URLs of a page together.
	MaxTotal = 16 << 20
	// maxImportDepth bounds how deep stylesheets' @import rules are
	// followed.
	maxImportDepth = 4
)

// Missing is a reference that wasn't inlined, and why.
type Missing struct {
	Ref    string `json:"ref" doc:"The reference as the page has it."`
	Reason string `json:"reason" doc:"Why it wasn't inlined, e.g. not found, or not an image."`
}

// Page is a page with what it refers to inlined.
type Page struct {
	HTML    string
	Missing []Missing
}

// kind is what a reference is used as, which decides the files it may
// name.
type kind int

const (
	kindStylesheet kind = iota
	kindScript
	kindImage
	// kindCSSResource is a url() in a stylesheet: an image or a font.
	kindCSSResource
	kindMedia
)

func (k kind) String() string {
	switch k {
	case kindStylesheet:
		return "a stylesheet"
	case kindScript:
		return "a script"
	case kindImage:
		return "an image"
	case kindCSSResource:
		return "an image or a font"
	case kindMedia:
		return "an audio or video file"
	}
	return "an asset"
}

// assetTypes are the files a page can have inlined, by extension: their
// media type and what they can be used as. Anything else (a page naming
// its .env as a script, say) is left out.
var assetTypes = map[string]struct {
	mediaType string
	kind      kind
}{
	".css":   {"text/css;charset=utf-8", kindStylesheet},
	".js":    {"text/javascript;charset=utf-8", kindScript},
	".mjs":   {"text/javascript;charset=utf-8", kindScript},
	".cjs":   {"text/javascript;charset=utf-8", kindScript},
	".png":   {"image/png", kindImage},
	".apng":  {"image/apng", kindImage},
	".jpg":   {"image/jpeg", kindImage},
	".jpeg":  {"image/jpeg", kindImage},
	".gif":   {"image/gif", kindImage},
	".webp":  {"image/webp", kindImage},
	".avif":  {"image/avif", kindImage},
	".svg":   {"image/svg+xml;charset=utf-8", kindImage},
	".ico":   {"image/x-icon", kindImage},
	".cur":   {"image/x-icon", kindImage},
	".bmp":   {"image/bmp", kindImage},
	".woff":  {"font/woff", kindCSSResource},
	".woff2": {"font/woff2", kindCSSResource},
	".ttf":   {"font/ttf", kindCSSResource},
	".otf":   {"font/otf", kindCSSResource},
	".mp4":   {"video/mp4", kindMedia},
	".webm":  {"video/webm", kindMedia},
	".ogv":   {"video/ogg", kindMedia},
	".mp3":   {"audio/mpeg", kindMedia},
	".m4a":   {"audio/mp4", kindMedia},
	".aac":   {"audio/aac", kindMedia},
	".oga":   {"audio/ogg", kindMedia},
	".ogg":   {"audio/ogg", kindMedia},
	".wav":   {"audio/wav", kindMedia},
	".flac":  {"audio/flac", kindMedia},
}

// fits reports whether a file of kind file can be used as use.
func (use kind) fits(file kind) bool {
	if use == kindCSSResource {
		return file == kindImage || file == kindCSSResource
	}
	return use == file
}

// storageShim gives the page in-memory localStorage, sessionStorage and
// document.cookie where reading them throws, as in an iframe sandboxed
// without allow-same-origin, so a page that keeps a high score there still
// runs. It goes before the page's first element, ahead of its scripts.
const storageShim = `<script>(()=>{const m=()=>{const s=new Map();return{get length(){return s.size},key:i=>[...s.keys()][i]??null,getItem:k=>s.has(String(k))?s.get(String(k)):null,setItem:(k,v)=>{s.set(String(k),String(v))},removeItem:k=>{s.delete(String(k))},clear:()=>{s.clear()}}};for(const n of["localStorage","sessionStorage"]){try{window[n]}catch{Object.defineProperty(window,n,{value:m(),configurable:true})}}try{document.cookie}catch{const c=new Map();Object.defineProperty(document,"cookie",{configurable:true,get:()=>[...c].map(([k,v])=>k+"="+v).join("; "),set:v=>{const p=String(v).split(";")[0],i=p.indexOf("=");if(i>0)c.set(p.slice(0,i).trim(),p.slice(i+1).trim())}})}})();</script>`

// Inline returns the page at name in fsys, whose content is page, with the
// files it refers to by relative path inlined. Paths are resolved from the
// page's directory, and a path starting with "/" from the root of fsys.
// Files outside fsys, hidden ones (a segment starting with "."), files of a
// type that doesn't fit their use, and files over MaxAsset are left as they
// are and listed in Missing.
func Inline(fsys fs.FS, name string, page []byte) (Page, error) {
	in := &inliner{fsys: fsys, budget: MaxTotal, dataURLs: map[string]string{}}
	var out bytes.Buffer
	z := html.NewTokenizer(bytes.NewReader(page))
	dir := path.Dir(name)
	shimmed := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if err := z.Err(); !errors.Is(err, io.EOF) {
				return Page{}, fmt.Errorf("parse %s: %w", name, err)
			}
			break
		}
		raw := bytes.Clone(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		tok := z.Token()
		if !shimmed && tok.DataAtom != atom.Html && tok.DataAtom != atom.Head {
			out.WriteString(storageShim)
			shimmed = true
		}
		if in.rewriteTag(&tok, dir) {
			out.WriteString(tok.String())
		} else {
			out.Write(raw)
		}
		if tt == html.StartTagToken && tok.DataAtom == atom.Style {
			// The stylesheet is the next token: raw text up to </style>.
			if z.Next() == html.TextToken {
				out.WriteString(in.rewriteCSS(string(z.Raw()), dir, 0, nil))
			} else {
				out.Write(z.Raw())
			}
		}
	}
	return Page{HTML: out.String(), Missing: in.missing}, nil
}

type inliner struct {
	fsys fs.FS
	// budget is what is left of MaxTotal.
	budget int
	// dataURLs are the files read so far, by path.
	dataURLs map[string]string
	missing  []Missing
}

// rewriteTag inlines what tok refers to. It reports whether it changed it.
func (in *inliner) rewriteTag(tok *html.Token, dir string) bool {
	changed := false
	set := func(key string, use kind) {
		for i := range tok.Attr {
			if tok.Attr[i].Namespace == "" && tok.Attr[i].Key == key {
				if value, ok := in.resolve(tok.Attr[i].Val, dir, use); ok {
					tok.Attr[i].Val = value
					changed = true
				}
			}
		}
	}
	switch tok.Data {
	case "link":
		rels := strings.Fields(strings.ToLower(attr(tok, "rel")))
		switch {
		case slices.Contains(rels, "stylesheet"):
			set("href", kindStylesheet)
		case slices.Contains(rels, "icon") || slices.Contains(rels, "apple-touch-icon"):
			set("href", kindImage)
		}
	case "script":
		set("src", kindScript)
	case "img":
		set("src", kindImage)
		changed = in.rewriteSrcset(tok, dir) || changed
	case "source":
		// In a <picture> a source is an image (it has a srcset); in a
		// <video> or <audio> it is media.
		if attr(tok, "srcset") != "" {
			changed = in.rewriteSrcset(tok, dir) || changed
		} else {
			set("src", kindMedia)
		}
	case "video":
		set("src", kindMedia)
		set("poster", kindImage)
	case "audio":
		set("src", kindMedia)
	case "input":
		if strings.EqualFold(attr(tok, "type"), "image") {
			set("src", kindImage)
		}
	}
	for i := range tok.Attr {
		if tok.Attr[i].Namespace == "" && tok.Attr[i].Key == "style" && strings.Contains(strings.ToLower(tok.Attr[i].Val), "url(") {
			if css := in.rewriteCSS(tok.Attr[i].Val, dir, 0, nil); css != tok.Attr[i].Val {
				tok.Attr[i].Val = css
				changed = true
			}
		}
	}
	return changed
}

// rewriteSrcset inlines the image candidates of tok's srcset.
func (in *inliner) rewriteSrcset(tok *html.Token, dir string) bool {
	for i := range tok.Attr {
		if tok.Attr[i].Namespace != "" || tok.Attr[i].Key != "srcset" {
			continue
		}
		candidates := parseSrcset(tok.Attr[i].Val)
		changed := false
		for j := range candidates {
			if value, ok := in.resolve(candidates[j].url, dir, kindImage); ok {
				candidates[j].url = value
				changed = true
			}
		}
		if !changed {
			return false
		}
		parts := make([]string, len(candidates))
		for j, c := range candidates {
			parts[j] = strings.TrimSpace(c.url + " " + c.descriptor)
		}
		tok.Attr[i].Val = strings.Join(parts, ", ")
		return true
	}
	return false
}

type srcsetCandidate struct {
	url, descriptor string
}

// parseSrcset splits a srcset into its candidates: a URL, then
// descriptors up to the next comma. A URL can contain commas, but not end
// with one.
func parseSrcset(srcset string) []srcsetCandidate {
	var candidates []srcsetCandidate
	s := srcset
	for {
		s = strings.TrimLeft(s, " \t\n\r\f,")
		if s == "" {
			return candidates
		}
		end := strings.IndexAny(s, " \t\n\r\f")
		if end < 0 {
			end = len(s)
		}
		u := s[:end]
		s = s[end:]
		if trimmed := strings.TrimRight(u, ","); trimmed != u {
			candidates = append(candidates, srcsetCandidate{url: trimmed})
			continue
		}
		descriptor, rest, _ := strings.Cut(s, ",")
		candidates = append(candidates, srcsetCandidate{url: u, descriptor: strings.TrimSpace(descriptor)})
		s = rest
	}
}

var (
	cssURLRe    = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^\s"')]*))\s*\)`)
	cssImportRe = regexp.MustCompile(`(?i)@import\s+(?:url\(\s*(?:"([^"]*)"|'([^']*)'|([^\s"')]*))\s*\)|"([^"]*)"|'([^']*)')`)
)

// firstGroup returns the first non-empty capture group of a match.
func firstGroup(groups []string) string {
	for _, g := range groups[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// rewriteCSS inlines what a stylesheet in dir refers to: the stylesheets it
// imports (with what they refer to, up to maxImportDepth deep) and the
// images and fonts of its url()s. importing holds the stylesheets being
// imported, which aren't imported again.
func (in *inliner) rewriteCSS(css, dir string, depth int, importing map[string]bool) string {
	css = cssImportRe.ReplaceAllStringFunc(css, func(match string) string {
		ref := firstGroup(cssImportRe.FindStringSubmatch(match))
		file, ok := in.lookup(ref, dir, kindStylesheet)
		if !ok {
			return match
		}
		if depth >= maxImportDepth || importing[file] {
			in.miss(ref, "imported too deep, or imported by itself")
			return match
		}
		data, ok := in.read(ref, file)
		if !ok {
			return match
		}
		nested := map[string]bool{file: true}
		for f := range importing {
			nested[f] = true
		}
		imported := in.rewriteCSS(string(data), path.Dir(file), depth+1, nested)
		value, ok := in.encode(ref, assetTypes[".css"].mediaType, []byte(imported))
		if !ok {
			return match
		}
		return `@import url("` + value + `")`
	})
	return cssURLRe.ReplaceAllStringFunc(css, func(match string) string {
		ref := firstGroup(cssURLRe.FindStringSubmatch(match))
		value, ok := in.resolve(ref, dir, kindCSSResource)
		if !ok {
			return match
		}
		return `url("` + value + `")`
	})
}

// resolve returns the data: URL for ref, a reference in dir used as use,
// or false when ref isn't a relative path or can't be inlined.
func (in *inliner) resolve(ref, dir string, use kind) (string, bool) {
	file, ok := in.lookup(ref, dir, use)
	if !ok {
		return "", false
	}
	fragment := ""
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		// An SVG sprite's symbol.
		fragment = ref[i:]
	}
	if value, ok := in.dataURLs[file]; ok {
		return in.charge(ref, value+fragment)
	}
	data, ok := in.read(ref, file)
	if !ok {
		return "", false
	}
	if use == kindStylesheet {
		data = []byte(in.rewriteCSS(string(data), path.Dir(file), 1, map[string]bool{file: true}))
	}
	value, ok := in.encode(ref, assetTypes[strings.ToLower(path.Ext(file))].mediaType, data)
	if !ok {
		return "", false
	}
	in.dataURLs[file] = value
	return value + fragment, true
}

// lookup resolves ref, a reference in dir used as use, to a file in fsys.
// References that aren't relative paths (a URL, a fragment, a data: URL)
// are left alone; ones that can't be inlined are listed as missing.
func (in *inliner) lookup(ref, dir string, use kind) (string, bool) {
	ref = strings.TrimSpace(ref)
	if !isRelative(ref) {
		return "", false
	}
	p := ref
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "", false
	}
	if unescaped, err := url.PathUnescape(p); err == nil {
		p = unescaped
	}
	var file string
	if strings.HasPrefix(p, "/") {
		file = path.Clean(strings.TrimLeft(p, "/"))
	} else {
		file = path.Join(dir, p)
	}
	if file == ".." || strings.HasPrefix(file, "../") || !fs.ValidPath(file) {
		in.miss(ref, "outside the working directory")
		return "", false
	}
	for _, segment := range strings.Split(file, "/") {
		if strings.HasPrefix(segment, ".") {
			in.miss(ref, "hidden")
			return "", false
		}
	}
	asset, known := assetTypes[strings.ToLower(path.Ext(file))]
	if !known || !use.fits(asset.kind) {
		in.miss(ref, "not "+use.String())
		return "", false
	}
	return file, true
}

// read returns the content of file, a regular file of at most MaxAsset
// bytes, which ref refers to.
func (in *inliner) read(ref, file string) ([]byte, bool) {
	info, err := fs.Stat(in.fsys, file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		in.miss(ref, "not found")
		return nil, false
	case err != nil:
		// A symlink that leads out of the working directory, say.
		in.miss(ref, "can't be read")
		return nil, false
	case !info.Mode().IsRegular():
		// Checked before it is opened: opening a FIFO would wait for a
		// writer.
		in.miss(ref, "not a file")
		return nil, false
	case info.Size() > MaxAsset:
		in.miss(ref, fmt.Sprintf("larger than %d MiB", MaxAsset>>20))
		return nil, false
	}
	f, err := in.fsys.Open(file)
	if err != nil {
		in.miss(ref, "can't be read")
		return nil, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxAsset+1))
	if err != nil {
		in.miss(ref, "can't be read")
		return nil, false
	}
	if len(data) > MaxAsset {
		in.miss(ref, fmt.Sprintf("larger than %d MiB", MaxAsset>>20))
		return nil, false
	}
	return data, true
}

// encode returns data as a data: URL, charged to the page's budget.
func (in *inliner) encode(ref, mediaType string, data []byte) (string, bool) {
	return in.charge(ref, "data:"+mediaType+";base64,"+base64.StdEncoding.EncodeToString(data))
}

// charge takes value's size from the page's budget. A value that doesn't
// fit is left out.
func (in *inliner) charge(ref, value string) (string, bool) {
	if len(value) > in.budget {
		in.miss(ref, fmt.Sprintf("the page's %d MiB of inlined files are used up", MaxTotal>>20))
		return "", false
	}
	in.budget -= len(value)
	return value, true
}

// miss lists ref as missing, once per reason.
func (in *inliner) miss(ref, reason string) {
	m := Missing{Ref: ref, Reason: reason}
	if !slices.Contains(in.missing, m) {
		in.missing = append(in.missing, m)
	}
}

// schemeRe matches a reference that starts with a URL scheme.
var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// isRelative reports whether ref is a path to a file, rather than a URL
// with a scheme (https:, data:, ...), a protocol-relative URL or a
// fragment.
func isRelative(ref string) bool {
	return ref != "" && !strings.HasPrefix(ref, "#") && !strings.HasPrefix(ref, "//") && !schemeRe.MatchString(ref)
}

func attr(tok *html.Token, key string) string {
	for _, a := range tok.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}
