package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Web-level mirrors of the Python version's photo tests.

var (
	jpegBytes = append([]byte{0xff, 0xd8, 0xff, 0xe0}, []byte("fake-jpeg-data")...)
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), []byte("fake-png-data")...)
	webpBytes = append([]byte("RIFF\x04\x00\x00\x00WEBP"), []byte("x")...)
	heicBytes = append([]byte("\x00\x00\x00\x18ftypheic"), []byte("fake-heic")...)
)

func (c *testClient) uploadPhoto(deviceID, kind, filename string, data []byte) response {
	return c.uploadPhotoReferer(deviceID, kind, filename, data, "")
}

func (c *testClient) uploadPhotoReferer(deviceID, kind, filename string, data []byte, referer string) response {
	c.t.Helper()
	token := c.csrfFor("/devices/" + deviceID)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("csrf_token", token); err != nil {
		c.t.Fatal(err)
	}
	if err := mw.WriteField("kind", kind); err != nil {
		c.t.Fatal(err)
	}
	if filename != "" {
		part, err := mw.CreateFormFile("photo", filename)
		if err != nil {
			c.t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			c.t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		c.t.Fatal(err)
	}
	headers := map[string]string{}
	if referer != "" {
		headers["Referer"] = referer
	}
	return c.doRaw("POST", "/devices/"+deviceID+"/photos",
		mw.FormDataContentType(), buf.Bytes(), headers)
}

func setupDevice(t *testing.T) (*testClient, string) {
	t.Helper()
	_, c, instanceDir := newTestServerDirs(t)
	c.setupAdmin()
	c.createDevice(nil)
	return c, instanceDir
}

func TestPhotoUploadViewAndHistory(t *testing.T) {
	c, instanceDir := setupDevice(t)
	resp := c.uploadPhoto("1", "front", "IMG_0001.jpg", jpegBytes)
	if resp.status != http.StatusFound {
		t.Fatalf("upload = %d, body %q", resp.status, resp.body)
	}
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "写真追加: 正面") {
		t.Error("photo history entry missing on detail")
	}
	if !strings.Contains(detail.body, "/devices/1/photos/1") {
		t.Error("photo link missing on detail")
	}
	view := c.get("/devices/1/photos/1")
	if view.status != http.StatusOK || view.body != string(jpegBytes) {
		t.Errorf("photo view = %d, bytes match = %v", view.status,
			view.body == string(jpegBytes))
	}
	if ct := view.header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("photo content type = %q", ct)
	}
	if h := view.header.Get("X-Content-Type-Options"); h != "nosniff" {
		t.Errorf("nosniff header = %q", h)
	}
	// The file is on disk under photos/<device>/.
	entries, err := os.ReadDir(filepath.Join(instanceDir, "photos", "1"))
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".jpg") {
		t.Errorf("photo dir = %v, %v", entries, err)
	}
	// The list shows the count.
	if body := c.get("/").body; !strings.Contains(body, "写真 1枚") {
		t.Error("list does not show the photo count")
	}
}

func TestPhotoJPEGNamesAndKinds(t *testing.T) {
	c, _ := setupDevice(t)
	for _, name := range []string{"a.jpg", "b.jpeg", "C.JPG"} {
		if resp := c.uploadPhoto("1", "back", name, jpegBytes); resp.status != http.StatusFound {
			t.Fatalf("upload %s = %d", name, resp.status)
		}
	}
	if resp := c.uploadPhoto("1", "damage", "d.png", pngBytes); resp.status != http.StatusFound {
		t.Fatalf("png upload = %d", resp.status)
	}
	if resp := c.uploadPhoto("1", "other", "e.webp", webpBytes); resp.status != http.StatusFound {
		t.Fatalf("webp upload = %d", resp.status)
	}
	detail := c.get("/devices/1")
	for _, want := range []string{"写真追加: 背面", "写真追加: 傷", "写真追加: その他"} {
		if !strings.Contains(detail.body, want) {
			t.Errorf("history missing %q", want)
		}
	}
}

func TestPhotoFiveLimit(t *testing.T) {
	c, _ := setupDevice(t)
	for i := 0; i < 5; i++ {
		if resp := c.uploadPhoto("1", "front", "p.jpg", jpegBytes); resp.status != http.StatusFound {
			t.Fatalf("upload %d = %d", i, resp.status)
		}
	}
	c.uploadPhoto("1", "front", "p.jpg", jpegBytes)
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "写真は1台あたり5枚までです。") {
		t.Error("limit flash missing")
	}
	if strings.Count(detail.body, "写真追加: 正面") != 5 {
		t.Error("more than 5 photos were stored")
	}
}

func TestPhotoRejectsBadContent(t *testing.T) {
	c, _ := setupDevice(t)
	cases := []struct {
		name     string
		filename string
		data     []byte
		want     string
	}{
		{"text as jpg", "x.jpg", []byte("not an image"), "写真は JPEG / PNG / WebP のファイルだけです"},
		{"png named jpg", "x.jpg", pngBytes, "写真は JPEG / PNG / WebP のファイルだけです"},
		{"jpg named png", "x.png", jpegBytes, "写真は JPEG / PNG / WebP のファイルだけです"},
		{"gif name", "x.gif", jpegBytes, "写真のファイル名は .jpg / .jpeg / .png / .webp / .heic / .heif のものだけです。"},
		{"no extension", "photo", jpegBytes, "写真のファイル名は"},
		{"heic name png content", "x.heic", pngBytes, "写真は JPEG / PNG / WebP のファイルだけです"},
	}
	for _, tc := range cases {
		c.uploadPhoto("1", "front", tc.filename, tc.data)
		detail := c.get("/devices/1")
		if !strings.Contains(detail.body, tc.want) {
			t.Errorf("%s: flash %q not shown", tc.name, tc.want)
		}
		if strings.Contains(detail.body, "写真追加") {
			t.Errorf("%s: a photo was stored", tc.name)
		}
	}
}

func TestPhotoHEICRules(t *testing.T) {
	c, _ := setupDevice(t)
	// An iPhone name with converted JPEG content is accepted.
	resp := c.uploadPhoto("1", "front", "IMG_0001.HEIC", jpegBytes)
	if resp.status != http.StatusFound {
		t.Fatalf("heic-name jpeg upload = %d", resp.status)
	}
	view := c.get("/devices/1/photos/1")
	if view.header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("converted photo content type = %q", view.header.Get("Content-Type"))
	}
	// Real HEIC content gets its own message, whatever the name.
	for _, name := range []string{"real.heic", "real.jpg"} {
		c.uploadPhoto("1", "front", name, heicBytes)
		detail := c.get("/devices/1")
		if !strings.Contains(detail.body, "写真が HEIC 形式でした") {
			t.Errorf("%s: HEIC message missing", name)
		}
	}
}

func TestPhotoOversize(t *testing.T) {
	c, _ := setupDevice(t)
	big := make([]byte, 5*1024*1024+1)
	copy(big, jpegBytes)
	resp := c.uploadPhoto("1", "front", "big.jpg", big)
	if resp.status != http.StatusFound {
		t.Fatalf("oversize upload = %d", resp.status)
	}
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "写真は1枚5MBまでです。") {
		t.Error("oversize flash missing")
	}
}

func TestPhotoDelete(t *testing.T) {
	c, instanceDir := setupDevice(t)
	c.uploadPhoto("1", "front", "a.jpg", jpegBytes)
	resp := c.post("/devices/1/photos/1/delete", "/devices/1", nil)
	if resp.status != http.StatusFound {
		t.Fatalf("delete = %d", resp.status)
	}
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "写真を削除しました。") ||
		!strings.Contains(detail.body, "写真削除: 正面") {
		t.Error("delete flash or history missing")
	}
	if resp := c.get("/devices/1/photos/1"); resp.status != http.StatusNotFound {
		t.Errorf("view after delete = %d, want 404", resp.status)
	}
	entries, _ := os.ReadDir(filepath.Join(instanceDir, "photos", "1"))
	if len(entries) != 0 {
		t.Errorf("photo file remains: %v", entries)
	}
}

func TestPhotoViewGuards(t *testing.T) {
	c, _ := setupDevice(t)
	c.createDevice(map[string]string{"serial_number": "SN0002"})
	c.uploadPhoto("1", "front", "a.jpg", jpegBytes)
	// A photo under another device's URL is a 404.
	if resp := c.get("/devices/2/photos/1"); resp.status != http.StatusNotFound {
		t.Errorf("cross-device view = %d, want 404", resp.status)
	}
	// Logged out: redirect to login.
	c.logout()
	if resp := c.get("/devices/1/photos/1"); resp.status != http.StatusFound {
		t.Errorf("logged-out view = %d, want 302", resp.status)
	}
}

func TestPhotoHardLimit413(t *testing.T) {
	c, _ := setupDevice(t)
	// Over the hard cap (5 MB + 64 KB): a flash and a redirect back
	// to the referring page on this host.
	big := make([]byte, 5*1024*1024+100*1024)
	copy(big, jpegBytes)
	resp := c.uploadPhotoReferer("1", "front", "big.jpg", big,
		c.base+"/devices/1")
	if resp.status != http.StatusFound || resp.location != c.base+"/devices/1" {
		t.Errorf("hard-limit response = %d %q", resp.status, resp.location)
	}
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "送信が大きすぎます。写真は1枚5MBまでです。") {
		t.Error("hard-limit flash missing")
	}
	// An external referer is not used; the fallback is the list.
	resp = c.uploadPhotoReferer("1", "front", "big.jpg", big,
		"https://evil.example/devices/1")
	if resp.location != "/" {
		t.Errorf("external-referer redirect = %q, want /", resp.location)
	}
}

func TestPhotoSimultaneousUploadsKeepLimit(t *testing.T) {
	_, c, _ := newTestServerDirs(t)
	c.setupAdmin()
	c.createDevice(nil)
	for i := 0; i < 4; i++ {
		c.uploadPhoto("1", "front", "p.jpg", jpegBytes)
	}
	// Six more uploads race for the one remaining slot (the same
	// session is fine; each upload is an independent request, and
	// the write transaction serializes the count check).
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.uploadPhoto("1", "front", "race.jpg", jpegBytes)
		}()
	}
	wg.Wait()
	detail := c.get("/devices/1")
	if n := strings.Count(detail.body, "写真追加: 正面"); n != 5 {
		t.Errorf("photo count after race = %d, want 5", n)
	}
}
