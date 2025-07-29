package main

import (
	"bytes"
	"crypto/md5"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"
)

//go:embed index.html
var indexHTML []byte

//go:embed style.css
var styleCSS []byte

//go:embed script.js
var scriptJS []byte

type Image struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	ModifiedDate int64  `json:"modified_date"`
	Hash         []int  `json:"hash"`
	Similarity   int    `json:"similarity"`
}

type ExifData struct {
	DateTaken   string `json:"date_taken"`
	CameraMake  string `json:"camera_make"`
	CameraModel string `json:"camera_model"`
	FStop       string `json:"fstop"`
	Subject     string `json:"subject"`
	HasExif     bool   `json:"has_exif"`
}

type ImageWithExif struct {
	Image
	ExifData
	Score int `json:"score"`
}

type GroupResponse struct {
	GroupSimilarityScore float64         `json:"group_similarity_score"`
	Images               []ImageWithExif `json:"images"`
}

var (
	groups         [][]Image
	imageRoot      string
	duplicatesFile string
	port           string
	tempDir        string
	cr2Cache       = make(map[string]string) // Map CR2 path to JPG temp path
)

// Simple XMP Subject extractor
func extractXMPSubject(data []byte) string {
	// Look for XMP data in the file
	xmpStart := bytes.Index(data, []byte("<x:xmpmeta"))
	if xmpStart == -1 {
		xmpStart = bytes.Index(data, []byte("<?xpacket"))
	}
	if xmpStart == -1 {
		return ""
	}

	xmpEnd := bytes.Index(data[xmpStart:], []byte("</x:xmpmeta>"))
	if xmpEnd == -1 {
		xmpEnd = bytes.Index(data[xmpStart:], []byte("<?xpacket end="))
		if xmpEnd != -1 {
			xmpEnd += 100 // give some buffer for the end tag
		}
	}
	if xmpEnd == -1 {
		return ""
	}

	xmpData := data[xmpStart : xmpStart+xmpEnd]

	// Look for Subject in RDF list format first (most common)
	if start := bytes.Index(xmpData, []byte("<rdf:li>")); start != -1 {
		start += 8 // len("<rdf:li>")
		if end := bytes.Index(xmpData[start:], []byte("</rdf:li>")); end != -1 {
			subject := string(xmpData[start : start+end])
			subject = strings.TrimSpace(subject)
			if subject != "" {
				return subject
			}
		}
	}

	// Look for Subject in other XMP formats
	patterns := [][]byte{
		[]byte("<dc:subject>"),
		[]byte("dc:subject=\""),
		[]byte("<photoshop:Headline>"),
		[]byte("photoshop:Headline=\""),
	}

	for _, pattern := range patterns {
		if start := bytes.Index(xmpData, pattern); start != -1 {
			start += len(pattern)
			var end int

			if bytes.HasSuffix(pattern, []byte(">")) {
				// XML tag format
				end = bytes.Index(xmpData[start:], []byte("</"))
			} else {
				// Attribute format
				end = bytes.Index(xmpData[start:], []byte("\""))
			}

			if end != -1 {
				subject := string(xmpData[start : start+end])
				subject = strings.TrimSpace(subject)
				if subject != "" {
					return subject
				}
			}
		}
	}

	return ""
}

// CR2 to JPG conversion functions
func isCR2File(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".cr2"
}

func generateTempJPGPath(cr2Path string) string {
	hash := md5.Sum([]byte(cr2Path))
	hashStr := hex.EncodeToString(hash[:])
	return filepath.Join(tempDir, hashStr+".jpg")
}

func convertCR2ToJPG(cr2Path string) (string, error) {
	// Check if we already have a cached version
	if jpgPath, exists := cr2Cache[cr2Path]; exists {
		if _, err := os.Stat(jpgPath); err == nil {
			return jpgPath, nil
		}
		// Cache entry exists but file is gone, remove from cache
		delete(cr2Cache, cr2Path)
	}

	jpgPath := generateTempJPGPath(cr2Path)

	// Check if ImageMagick is available (try 'magick' first, then 'convert')
	var cmdName string
	if _, err := exec.LookPath("magick"); err == nil {
		cmdName = "magick"
	} else if _, err := exec.LookPath("convert"); err == nil {
		cmdName = "convert"
	} else {
		return "", fmt.Errorf("ImageMagick not found: neither 'magick' nor 'convert' command available")
	}

	// Convert CR2 to JPG using ImageMagick
	cmd := exec.Command(cmdName, cr2Path, "-quality", "85", "-resize", "2048x2048>", jpgPath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to convert CR2 to JPG: %v", err)
	}

	// Cache the result
	cr2Cache[cr2Path] = jpgPath
	log.Printf("Converted CR2 to JPG: %s -> %s", filepath.Base(cr2Path), filepath.Base(jpgPath))

	return jpgPath, nil
}

func cleanupTempFiles() {
	if tempDir != "" {
		os.RemoveAll(tempDir)
	}
}

func loadGroups() {
	f, err := os.Open(duplicatesFile)
	if err != nil {
		log.Fatalf("Failed to open %s: %v", duplicatesFile, err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&groups); err != nil {
		log.Fatalf("Failed to decode %s: %v", duplicatesFile, err)
	}
}

func getExif(path string) ExifData {
	f, err := os.Open(path)
	if err != nil {
		return ExifData{HasExif: false}
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return ExifData{HasExif: false}
	}

	// Try to extract Subject from XMP data first
	xmpSubject := extractXMPSubject(data)

	rawExif, err := exif.SearchAndExtractExif(data)
	if err != nil {
		// If no EXIF but we found XMP subject, return that
		if xmpSubject != "" {
			return ExifData{HasExif: true, Subject: xmpSubject}
		}
		return ExifData{HasExif: false}
	}
	ti := exif.NewTagIndex()
	if err := exif.LoadStandardTags(ti); err != nil {
		return ExifData{HasExif: false}
	}

	// Use the proper API for collecting EXIF data
	ifdMapping, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return ExifData{HasExif: false}
	}

	_, index, err := exif.Collect(ifdMapping, ti, rawExif)
	if err != nil {
		return ExifData{HasExif: false}
	}
	rootIfd := index.RootIfd
	var dateTaken, cameraMake, cameraModel, subject string

	// Helper to get first string value from tag entries
	getFirst := func(entries []*exif.IfdTagEntry) string {
		if len(entries) > 0 {
			if s, err := entries[0].FormatFirst(); err == nil {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}

	// Special helper for UserComment and other binary text fields
	getUserComment := func(entries []*exif.IfdTagEntry) string {
		if len(entries) > 0 {
			entry := entries[0]
			// Try to get the raw value first
			if rawValue, err := entry.Value(); err == nil {
				if strValue, ok := rawValue.(string); ok && strValue != "" {
					return strings.TrimSpace(strValue)
				}
				// For UserComment, the first 8 bytes are encoding info, rest is text
				if entry.TagName() == "UserComment" {
					if byteSlice, ok := rawValue.([]byte); ok && len(byteSlice) > 8 {
						// Skip the first 8 bytes (encoding header) and get the text
						textBytes := byteSlice[8:]
						// Remove null bytes and convert to string
						text := string(bytes.Trim(textBytes, "\x00"))
						if text != "" {
							return strings.TrimSpace(text)
						}
					}
				}
			}
			// Fallback to FormatFirst
			if s, err := entry.FormatFirst(); err == nil && s != "[ASCII]" && s != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}

	// Try to find EXIF sub-IFD
	var exifIfd *exif.Ifd
	if ifd, exists := index.Lookup["IFD/Exif"]; exists {
		exifIfd = ifd
	}

	// DateTimeOriginal - try both root and EXIF IFD
	if entries, err := rootIfd.FindTagWithName("DateTimeOriginal"); err == nil {
		dateTaken = getFirst(entries)
	} else if exifIfd != nil {
		if entries, err := exifIfd.FindTagWithName("DateTimeOriginal"); err == nil {
			dateTaken = getFirst(entries)
		}
	}
	// Camera Make
	if entries, err := rootIfd.FindTagWithName("Make"); err == nil {
		cameraMake = getFirst(entries)
	} else if exifIfd != nil {
		if entries, err := exifIfd.FindTagWithName("Make"); err == nil {
			cameraMake = getFirst(entries)
		}
	}
	// Camera Model
	if entries, err := rootIfd.FindTagWithName("Model"); err == nil {
		cameraModel = getFirst(entries)
	} else if exifIfd != nil {
		if entries, err := exifIfd.FindTagWithName("Model"); err == nil {
			cameraModel = getFirst(entries)
		}
	}

	// Subject - try XPSubject, Subject, UserComment, and ImageDescription
	// Note: XMP Subject data is not accessible via EXIF library
	if entries, err := rootIfd.FindTagWithName("XPSubject"); err == nil {
		subject = getFirst(entries)
	} else if exifIfd != nil {
		if entries, err := exifIfd.FindTagWithName("XPSubject"); err == nil {
			subject = getFirst(entries)
		}
	}

	if subject == "" {
		if entries, err := rootIfd.FindTagWithName("Subject"); err == nil {
			subject = getFirst(entries)
		} else if exifIfd != nil {
			if entries, err := exifIfd.FindTagWithName("Subject"); err == nil {
				subject = getFirst(entries)
			}
		}
	}

	// Try UserComment in EXIF IFD as another potential source
	if subject == "" && exifIfd != nil {
		if entries, err := exifIfd.FindTagWithName("UserComment"); err == nil {
			subject = getUserComment(entries)
		}
	}

	// Only use ImageDescription as last resort if it's not the generic camera description
	if subject == "" {
		if entries, err := rootIfd.FindTagWithName("ImageDescription"); err == nil {
			imageDesc := getFirst(entries)
			// Skip generic camera descriptions
			if imageDesc != "" && !strings.Contains(strings.ToUpper(imageDesc), "DIGITAL CAMERA") {
				subject = imageDesc
			}
		}
	}
	// Check if we actually found any EXIF data
	hasAnyExif := dateTaken != "" || cameraMake != "" || cameraModel != "" || subject != ""

	// Use XMP subject if we found one and EXIF subject is empty or generic
	if xmpSubject != "" && (subject == "" || subject == "[ASCII]" || strings.Contains(subject, "UserComment<")) {
		subject = xmpSubject
		hasAnyExif = true
	}

	return ExifData{
		DateTaken:   dateTaken,
		CameraMake:  cameraMake,
		CameraModel: cameraModel,
		FStop:       "", // Not handled here, add if needed
		Subject:     subject,
		HasExif:     hasAnyExif,
	}
}

func groupSimilarityScore(imgs []ImageWithExif) float64 {
	total := len(imgs)
	identical := 0
	for i := 0; i < total; i++ {
		for j := i + 1; j < total; j++ {
			if exifIdentical(imgs[i].ExifData, imgs[j].ExifData) {
				identical++
			}
		}
	}
	if total <= 1 {
		return 5.0
	}
	return float64(total) / float64(identical+1) * 5.0
}

func exifIdentical(a, b ExifData) bool {
	if a.CameraModel != b.CameraModel {
		return false
	}
	if a.FStop != b.FStop {
		return false
	}
	// Allow date taken to be within 1 hour
	at, err1 := time.Parse(time.RFC3339, a.DateTaken)
	bt, err2 := time.Parse(time.RFC3339, b.DateTaken)
	if err1 == nil && err2 == nil {
		delta := at.Sub(bt)
		if delta < 0 {
			delta = -delta
		}
		if delta > time.Hour {
			return false
		}
	}
	return true
}

func scoreImages(imgs []ImageWithExif) []ImageWithExif {
	maxRes := 0
	for _, img := range imgs {
		res := img.Width * img.Height
		if res > maxRes {
			maxRes = res
		}
	}
	allNoExif := true
	oldestIdx := 0
	oldest := int64(1<<63 - 1)
	for i := range imgs {
		// Base score for having EXIF data
		if imgs[i].HasExif {
			imgs[i].Score = 1
			allNoExif = false
		} else {
			imgs[i].Score = 0
		}

		// Bonus points for having a proper subject (higher priority)
		if imgs[i].Subject != "" {
			if !strings.Contains(imgs[i].Subject, "UserComment<") &&
				imgs[i].Subject != "[ASCII]" &&
				!strings.Contains(strings.ToUpper(imgs[i].Subject), "DIGITAL CAMERA") {
				imgs[i].Score += 2 // Significant bonus for meaningful subject
			}
		}

		// Bonus for highest resolution
		if imgs[i].Width*imgs[i].Height == maxRes {
			imgs[i].Score++
		}

		// Track oldest for fallback
		if imgs[i].ModifiedDate < oldest {
			oldest = imgs[i].ModifiedDate
			oldestIdx = i
		}
	}
	if allNoExif {
		imgs[oldestIdx].Score++
	}
	return imgs
}

func getRelativeImagePath(fullPath string) string {
	if strings.HasPrefix(fullPath, imageRoot) {
		return strings.TrimPrefix(fullPath, imageRoot+"/")
	}
	return fullPath
}

func groupHandler(w http.ResponseWriter, r *http.Request) {
	idx := 0
	if v := r.URL.Query().Get("idx"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			idx = n
		}
	}
	if idx < 0 || idx >= len(groups) {
		http.Error(w, "Group not found", 404)
		return
	}
	group := groups[idx]
	// Create a combined structure that keeps original path with each image
	type imageWithPaths struct {
		ImageWithExif
		OriginalPath string
	}

	var imgsWithPaths []imageWithPaths
	for _, img := range group {
		// Check if file still exists on disk before processing
		if _, err := os.Stat(img.Path); os.IsNotExist(err) {
			log.Printf("Skipping missing file: %s", img.Path)
			continue // Skip deleted files
		}

		exif := getExif(img.Path)
		relativePath := getRelativeImagePath(img.Path)
		imgWithExif := ImageWithExif{
			Image:    img,
			ExifData: exif,
		}
		imgWithExif.Path = relativePath // override path to be relative

		imgsWithPaths = append(imgsWithPaths, imageWithPaths{
			ImageWithExif: imgWithExif,
			OriginalPath:  img.Path,
		})
	}

	// If no images remain after filtering, return 404
	if len(imgsWithPaths) == 0 {
		http.Error(w, "No images found in group", 404)
		return
	}

	// Score the images
	var imgs []ImageWithExif
	for _, imgWithPath := range imgsWithPaths {
		imgs = append(imgs, imgWithPath.ImageWithExif)
	}
	imgs = scoreImages(imgs)

	// Update the scores back to our combined structure
	for i := range imgsWithPaths {
		imgsWithPaths[i].ImageWithExif.Score = imgs[i].Score
	}

	// Sort by score (highest first)
	sort.Slice(imgsWithPaths, func(i, j int) bool {
		return imgsWithPaths[i].ImageWithExif.Score > imgsWithPaths[j].ImageWithExif.Score
	})

	score := groupSimilarityScore(imgs)
	// Compose response with both images and original paths
	type frontendImage struct {
		ImageWithExif
		OriginalPath string `json:"original_path"`
	}
	var frontendImages []frontendImage
	for _, imgWithPath := range imgsWithPaths {
		frontendImages = append(frontendImages, frontendImage{
			ImageWithExif: imgWithPath.ImageWithExif,
			OriginalPath:  imgWithPath.OriginalPath,
		})
	}
	resp := struct {
		GroupSimilarityScore float64         `json:"group_similarity_score"`
		Images               []frontendImage `json:"images"`
	}{
		GroupSimilarityScore: score,
		Images:               frontendImages,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		Path string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if req.Path == "" {
		http.Error(w, "Path is required", 400)
		return
	}

	// Security check: ensure the path is within the image root directory
	if !strings.HasPrefix(req.Path, imageRoot) {
		log.Printf("Security violation: attempted to delete file outside image root: %s", req.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "File is outside allowed directory",
		})
		return
	}

	// Check if file exists
	if _, err := os.Stat(req.Path); os.IsNotExist(err) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "File does not exist",
		})
		return
	}

	// Delete the file
	if err := os.Remove(req.Path); err != nil {
		log.Printf("Error deleting file %s: %v", req.Path, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// If this was a CR2 file, clean up any cached JPG conversion
	if isCR2File(req.Path) {
		if jpgPath, exists := cr2Cache[req.Path]; exists {
			os.Remove(jpgPath) // Best effort cleanup, ignore errors
			delete(cr2Cache, req.Path)
			log.Printf("Cleaned up cached JPG for deleted CR2: %s", filepath.Base(jpgPath))
		}
	}

	log.Printf("Successfully deleted file: %s", req.Path)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// Static file handlers for embedded files
func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write(indexHTML)
}

func styleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css")
	w.Write(styleCSS)
}

func scriptHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Write(scriptJS)
}

// Custom image handler that converts CR2 files to JPG on-demand
func imageHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the image path from URL
	imagePath := strings.TrimPrefix(r.URL.Path, "/images/")
	fullPath := filepath.Join(imageRoot, imagePath)

	// Check if file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	// If it's a CR2 file, convert to JPG and serve the converted version
	if isCR2File(fullPath) {
		jpgPath, err := convertCR2ToJPG(fullPath)
		if err != nil {
			log.Printf("Failed to convert CR2 file %s: %v", fullPath, err)
			http.Error(w, "Failed to process CR2 file", http.StatusInternalServerError)
			return
		}

		// Serve the converted JPG file
		http.ServeFile(w, r, jpgPath)
		return
	}

	// For non-CR2 files, serve directly
	http.ServeFile(w, r, fullPath)
}

func main() {
	flag.StringVar(&imageRoot, "imagepath", "", "Root path for images to serve")
	flag.StringVar(&duplicatesFile, "duplicates", "groups.json", "Path to JSON file with duplicate groups")
	flag.StringVar(&port, "port", "8080", "Port to listen on")
	flag.Parse()
	if imageRoot == "" {
		log.Fatal("-imagepath flag is required")
	}

	// Initialize temp directory for CR2 conversions
	var err error
	tempDir, err = os.MkdirTemp("", "dupedeleter_cr2_*")
	if err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	log.Printf("Using temp directory for CR2 conversions: %s", tempDir)

	// Cleanup temp files on exit
	defer cleanupTempFiles()

	loadGroups()

	// API endpoints
	http.HandleFunc("/api/group", groupHandler)
	http.HandleFunc("/api/delete", deleteHandler)

	// Static file endpoints (embedded)
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/style.css", styleHandler)
	http.HandleFunc("/script.js", scriptHandler)

	// Image serving with CR2 conversion support
	http.HandleFunc("/images/", imageHandler)

	log.Printf("Listening on :%s, serving images from %s and loading duplicates from %s", port, imageRoot, duplicatesFile)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
