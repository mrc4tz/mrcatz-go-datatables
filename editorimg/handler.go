package editorimg

import (
	"errors"
	"net/http"

	"github.com/mrc4tz/mrcatz-go-datatables/httpdt"
)

// Handler membuat endpoint upload (multipart, nama field mis. "image") untuk
// `field.upload` editor di Vue. Pakai di Gin dengan gin.WrapF(imgs.Handler("image")).
//
//   - sukses → 201 {"url": "...", "path": "..."}
//   - *ImageError → 422 {"message": "...", "error": "Unprocessable Entity", "statusCode": 422}
//   - error lain → 500 {"statusCode": 500, "message": "Internal server error"}
//
// Body request dibatasi MaxSizeKB + 1 MB (overhead multipart).
func (m *Images) Handler(field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, m.maxBytes+1<<20)
		res, err := m.uploadRequest(r, field)
		if err == nil {
			httpdt.WriteJSON(w, http.StatusCreated, res)
			return
		}
		var ie *ImageError
		if errors.As(err, &ie) {
			httpdt.WriteJSON(w, ie.Status(), httpdt.NewErrorBody(ie.Status(), ie.Msg))
			return
		}
		httpdt.WriteJSON(w, http.StatusInternalServerError, httpdt.InternalErrorBody)
	}
}

func (m *Images) uploadRequest(r *http.Request, field string) (UploadResult, error) {
	mf, fh, err := r.FormFile(field)
	if mf != nil {
		_ = mf.Close() // dibuka ulang oleh FromMultipart
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return UploadResult{}, m.tooLarge()
	case errors.Is(err, http.ErrMissingFile), errors.Is(err, http.ErrNotMultipart):
		return m.Upload(r.Context(), nil)
	case err != nil:
		return UploadResult{}, err
	}
	f, err := FromMultipart(fh)
	if err != nil {
		return UploadResult{}, err
	}
	return m.Upload(r.Context(), f)
}
