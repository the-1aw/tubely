package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	r.ParseMultipartForm(10 << 20)
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse form file", err)
		return
	}
	defer file.Close()
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't find video", err)
		return
	}
	contentType := header.Header.Get("content-type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse media type", err)
		return
	}
	if mediaType != "image/jped" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type", fmt.Errorf("Unsuported media type %s", mediaType))
		return
	}
	splitContentType := strings.Split(contentType, "/")
	extension := splitContentType[len(splitContentType)-1]
	randBytes := make([]byte, 32)
	rand.Read(randBytes)
	filename := base64.RawURLEncoding.WithPadding(base64.NoPadding).EncodeToString(randBytes)
	path := filepath.Join(cfg.assetsRoot, fmt.Sprintf("%s.%s", filename, extension))
	thumbnailFile, err := os.Create(path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create thumbnail file", err)
		return
	}
	defer thumbnailFile.Close()
	io.Copy(thumbnailFile, file)
	thumbnailURL := fmt.Sprintf("http://localhost:%s/%s", cfg.port, path)
	video.ThumbnailURL = &thumbnailURL
	if cfg.db.UpdateVideo(video) != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video with thumbnail", err)
		return
	}
	respondWithJSON(w, http.StatusOK, video)
}
