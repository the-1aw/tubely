package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
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

func processVideoForFastStart(filePath string) (string, error) {
	fastStartPath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", fastStartPath)
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	return fastStartPath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	preSignClient := s3.NewPresignClient(s3Client)
	getObjectInput := s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}
	presignedRequest, err := preSignClient.PresignGetObject(context.TODO(), &getObjectInput, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}
	return presignedRequest.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	bucket, key, ok := strings.Cut(*video.VideoURL, ",")
	if !ok {
		return database.Video{}, fmt.Errorf("invalid video url")
	}
	presignedUrl, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Minute*5)
	if err != nil {
		return database.Video{}, err
	}
	video.VideoURL = &presignedUrl
	return video, nil
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
	fastStartVideoPath, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Fail generating fastStart", err)
	}
	fastStartFile, err := os.Open(fastStartVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Fail opening fastStartFile", err)
	}
	defer os.Remove(fastStartFile.Name())
	defer fastStartFile.Close()
	randBytes := make([]byte, 32)
	rand.Read(randBytes)
	bucketKey := fmt.Sprintf("%s.%s", hex.EncodeToString(randBytes), "mp4")
	cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(bucketKey),
		Body:        fastStartFile,
		ContentType: aws.String(mimeType),
	})
	videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, bucketKey)
	video.VideoURL = &videoURL
	cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}
	signedVideo, err := cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get signed video", err)
	}
	respondWithJSON(w, http.StatusOK, signedVideo)
}
