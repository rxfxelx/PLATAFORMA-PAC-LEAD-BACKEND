package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "github.com/go-chi/chi/v5"
)

// mountUpload registers the image upload endpoint on the given router. The
// route accepts multipart form requests containing a file under the key
// "image" and persists it to a local uploads directory. It returns a JSON
// object with a URL pointing to the saved file. The URL uses the same
// scheme and host as the incoming request. The upload directory can be
// configured via the UPLOAD_DIR environment variable (default: "uploads").
func (a *App) mountUpload(r chi.Router) {
    r.Post("/upload", a.uploadImage)
}

// uploadImage handles POST /api/upload. It reads the uploaded image from
// the multipart form, saves it with a unique filename in the configured
// upload directory and responds with a JSON containing the public URL.
func (a *App) uploadImage(w http.ResponseWriter, r *http.Request) {
    // Parse up to 10MB of incoming multipart data. Adjust size as needed.
    if err := r.ParseMultipartForm(10 << 20); err != nil {
        http.Error(w, "multipart parse error: "+err.Error(), http.StatusBadRequest)
        return
    }
    file, header, err := r.FormFile("image")
    if err != nil {
        http.Error(w, "image file required", http.StatusBadRequest)
        return
    }
    defer file.Close()

    // Ensure uploads directory exists. Use UPLOAD_DIR env or default.
    uploadDir := getenv("UPLOAD_DIR", "uploads")
    if err := os.MkdirAll(uploadDir, 0o755); err != nil {
        http.Error(w, "cannot create upload dir: "+err.Error(), http.StatusInternalServerError)
        return
    }
    // Determine file extension from original filename (fallback to .png).
    ext := strings.ToLower(filepath.Ext(header.Filename))
    if ext == "" {
        ext = ".png"
    }
    // Construct unique filename using timestamp to avoid collisions.
    // Use nanoseconds to reduce the chance of duplicates.
    filename := strconv.FormatInt(time.Now().UnixNano(), 10) + ext
    destPath := filepath.Join(uploadDir, filename)

    dst, err := os.Create(destPath)
    if err != nil {
        http.Error(w, "cannot save file: "+err.Error(), http.StatusInternalServerError)
        return
    }
    defer dst.Close()

    if _, err := io.Copy(dst, file); err != nil {
        http.Error(w, "write file error: "+err.Error(), http.StatusInternalServerError)
        return
    }
    // Build the full URL. Use the request's host and scheme.
    scheme := "http"
    if r.TLS != nil {
        scheme = "https"
    }
    // r.Host includes host and port
    url := fmt.Sprintf("%s://%s/uploads/%s", scheme, r.Host, filename)
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"url": url})
}