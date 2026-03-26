package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func getVideoAspectRatio(filepath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	var b []byte
	buff := bytes.NewBuffer(b)
	cmd.Stdout = buff
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Error running ffprobe: %v", err)
	}
	type probeJson struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	type streams struct {
		Streams []probeJson `json:"streams"`
	}
	var probeStream streams
	err = json.Unmarshal(buff.Bytes(), &probeStream)
	if err != nil {
		return "", fmt.Errorf("Error unmarshaling json: %v", err)
	}
	aspectRatio := probeStream.Streams[0].Width / probeStream.Streams[0].Height
	if aspectRatio == 1 {
		return "16:9", nil
	} else if aspectRatio == 0 {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filepath string) (string, error) {
	ofPath := filepath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filepath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", ofPath)
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Error running ffmpeg: %v", err)
	}
	return ofPath, nil
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 10 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)
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

	video, err := cfg.db.GetVideo(videoID)
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "You do not own this video", nil)
		return
	}
	mpFile, mpFileH, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't retrieve video file and header", err)
		return
	}
	defer mpFile.Close()
	mpType := mpFileH.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(mpType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing mime: %v", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusNotAcceptable, "Incorrect file type", nil)
		return
	}
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating file: %v", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, mpFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing to temp file", err)
		return
	}

	processedFileName, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing file", err)
		return
	}
	processedFile, err := os.Open(processedFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening processed file", err)
		return
	}
	defer processedFile.Close()

	videoAspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing to temp file", err)
		return
	}
	var fileNameAspectRatio string
	switch videoAspectRatio {
	case "16:9":
		fileNameAspectRatio = "landscape/"
	case "9:16":
		fileNameAspectRatio = "portrait/"
	default:
		fileNameAspectRatio = "other/"
	}

	videoFileNameBytes := make([]byte, 32)
	rand.Read(videoFileNameBytes)
	videoFileName := fileNameAspectRatio + base64.RawURLEncoding.EncodeToString(videoFileNameBytes) + ".mp4"
	videoURL := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, videoFileName)
	s3PutObjectInput := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoFileName,
		Body:        processedFile,
		ContentType: &mediaType,
	}
	cfg.s3Client.PutObject(r.Context(), &s3PutObjectInput)

	video.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
