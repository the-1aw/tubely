package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func authenticateUser(h http.Header, jwtSecret string) (uuid.UUID, error) {
	token, err := auth.GetBearerToken(h)
	if err != nil {
		return uuid.UUID{}, err
	}

	userID, err := auth.ValidateJWT(token, jwtSecret)
	if err != nil {
		return uuid.UUID{}, err
	}
	return userID, nil
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	userID, err := authenticateUser(r.Header, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't find video", err)
		return
	}
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	videofile, fileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse video", err)
		return
	}
	defer videofile.Close()
	mimeType, _, err := mime.ParseMediaType(fileHeader.Header.Get("content-type"))
	if err != nil || mimeType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type", err)
		return
	}
	tmpFile, err := os.CreateTemp(os.TempDir(), "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Fail creating tmpFile", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()
	io.Copy(tmpFile, videofile)
	tmpFile.Seek(0, io.SeekStart)
	randBytes := make([]byte, 32)
	rand.Read(randBytes)
	bucketKey := fmt.Sprintf("%s.%s", hex.EncodeToString(randBytes), "mp4")
	cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(bucketKey),
		Body:        tmpFile,
		ContentType: aws.String(mimeType),
	})
	s3URL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, bucketKey)
	video.VideoURL = &s3URL
	cfg.db.UpdateVideo(video)
}
