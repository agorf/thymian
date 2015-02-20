package thumbs

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sync"

	"github.com/cheggaaa/pb"
	_ "github.com/mattn/go-sqlite3"
)

const (
	thumbsDir = "public/thumbs"
	workers   = 4 // should be at least 1
)

var thumbsPath string

func generateThumb(photoPath, thumbPath, thumbSize string, crop bool) error {
	if _, err := os.Stat(thumbPath); err == nil { // file exists
		return err
	}

	vipsOpts := []string{
		"--rotate",
		"--size", thumbSize,
		"--interpolator", "bicubic",
		"--output", thumbPath + "[Q=97,no_subsample,strip]",
	}

	if crop {
		vipsOpts = append(vipsOpts, "--crop")
	}

	cmdArgs := append([]string{photoPath}, vipsOpts...)
	return exec.Command("vipsthumbnail", cmdArgs...).Run()
}

func generateThumbs(photoPath string) (err error) {
	smallThumbPhotoPath := photoPath

	identifier := fmt.Sprintf("%x", md5.Sum([]byte(photoPath)))
	thumbPathFmt := path.Join(thumbsPath, fmt.Sprintf("%s_%%s.jpg", identifier))
	bigThumbPath := fmt.Sprintf(thumbPathFmt, "big")
	smallThumbPath := fmt.Sprintf(thumbPathFmt, "small")

	err = generateThumb(photoPath, bigThumbPath, "1000", false)
	if err == nil {
		smallThumbPhotoPath = bigThumbPath // create small thumb from big for speed
	} else {
		log.Println("Failed to create", bigThumbPath, "for", photoPath, "with error:", err)
	}

	err = generateThumb(smallThumbPhotoPath, smallThumbPath, "200", true)
	if err != nil {
		log.Println("Failed to create", smallThumbPath, "for", photoPath, "with error:", err)
	}

	return
}

func GenerateThumbs(thymePath string) {
	var photosCount int

	dbPath := path.Join(os.Getenv("HOME"), ".thyme.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalln("Failed to open database:", err)
	}
	defer db.Close()

	rows, err := db.Query(`
	SELECT path FROM photos
	JOIN sets ON photos.set_id = sets.id
	ORDER BY sets.taken_at DESC, photos.taken_at ASC
	`)
	if err != nil {
		log.Fatalln("Failed to access table:", err)
	}
	defer rows.Close()

	err = db.QueryRow("SELECT COUNT(*) FROM photos").Scan(&photosCount)
	if err != nil {
		log.Fatalln("Failed to get photos count:", err)
	}

	thumbsPath, err = filepath.Abs(path.Join(thymePath, thumbsDir))
	if err != nil {
		log.Fatalln("Failed to resolve absolute path:", err)
	}

	err = os.MkdirAll(thumbsPath, os.ModeDir|0755)
	if err != nil {
		log.Fatalln("Failed to create thumbs path:", err)
	}

	// log to file because a progress bar is going to be rendered
	logFile, err := os.Create("thyme-generate-thumbs.log")
	if err == nil {
		log.SetOutput(logFile)
	}
	defer logFile.Close()

	ch := make(chan string)
	wg := sync.WaitGroup{}
	bar := pb.StartNew(photosCount)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			for photoPath := range ch {
				generateThumbs(photoPath)
				bar.Increment()
			}

			wg.Done()
		}()
	}

	for rows.Next() {
		var photoPath string
		if err := rows.Scan(&photoPath); err != nil {
			log.Fatalln("Failed to get photo path:", err)
		}
		ch <- photoPath
	}

	close(ch)
	wg.Wait()

	if err := rows.Err(); err != nil {
		log.Fatalln(err)
	}

	// Remove empty log file
	logFileInfo, err := logFile.Stat()
	if err == nil && logFileInfo.Size() == 0 {
		os.Remove(logFileInfo.Name())
	}
}
