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

	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse multipart data", err)
		return
	}
	mpFile, mpFileH, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't retrieve thumbnail file and header", err)
		return
	}
	defer mpFile.Close()
	mpType := mpFileH.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(mpType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing mime: %v", err)
		return
	}
	if (mediaType != "image/jpeg") && (mediaType != "image/png") {
		respondWithError(w, http.StatusNotAcceptable, "Incorrect file type", nil)
		return
	}
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't retrieve video", err)
		return
	}
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "You do not own this video", err)
		return
	}
	thumbFileNameBytes := make([]byte, 32)
	rand.Read(thumbFileNameBytes)
	thumbFileName := base64.RawURLEncoding.EncodeToString(thumbFileNameBytes)
	thumbFilePath := filepath.Join(cfg.assetsRoot, thumbFileName)
	thumbURL := fmt.Sprintf("http://localhost:%s/%s", cfg.port, thumbFilePath)
	video.ThumbnailURL = &thumbURL
	thumbFile, err := os.Create(thumbFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating thumbnail file", err)
		return
	}
	defer thumbFile.Close()
	_, err = io.Copy(thumbFile, mpFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing thumbnail file", err)
		return
	}
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video", err)
		return
	}
	respondWithJSON(w, http.StatusOK, video)
}
