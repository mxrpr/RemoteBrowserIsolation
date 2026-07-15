package main

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"rbi-go/internal/auth"
	"rbi-go/internal/rootca"
)

// rootCaResponse is the JSON body returned by GET and POST /api/admin/rootca.
// Field names match the C# RootCaResponse record serialised with ASP.NET Core
// camelCase policy. PfxBytes and PfxPassword are intentionally absent.
type rootCaResponse struct {
	Id         int64     `json:"id"`
	Subject    string    `json:"subject"`
	NotBefore  time.Time `json:"notBefore"`
	NotAfter   time.Time `json:"notAfter"`
	Thumbprint string    `json:"thumbprint"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// toRootCaResponse converts a RootCaRow into the JSON-ready rootCaResponse shape.
func toRootCaResponse(row *rootca.RootCaRow) rootCaResponse {
	return rootCaResponse{
		Id:         row.Id,
		Subject:    row.Subject,
		NotBefore:  row.NotBefore,
		NotAfter:   row.NotAfter,
		Thumbprint: row.Thumbprint,
		UploadedAt: row.UploadedAt,
	}
}

// handleGetRootCa handles GET /api/admin/rootca (JWT-protected).
// Returns current CA metadata as JSON, or 404 if no CA has been uploaded.
// Mirrors C# AdminRootCaEndpoints.MapGet("").
func handleGetRootCa(store *rootca.Store, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row, err := store.GetMetadata()
		if err != nil {
			slog.Error("rootca GET: get metadata", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if row == nil {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "No root CA is currently configured."})
			return
		}
		writeJSON(w, http.StatusOK, toRootCaResponse(row))
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handlePostRootCa handles POST /api/admin/rootca (JWT-protected).
// Accepts multipart/form-data with a 'pfx' file field and an optional 'password'
// field. Validates the PFX is a CA certificate with a private key, stores it
// (replacing any existing CA row), and clears both the CA cache and the mint cache.
// Mirrors C# AdminRootCaEndpoints.MapPost("").
func handlePostRootCa(store *rootca.Store, minter *rootca.Minter, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "multipart/form-data with 'pfx' file and 'password' field is required.",
			})
			return
		}

		pfxFile, _, err := r.FormFile("pfx")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "A 'pfx' file is required."})
			return
		}
		defer pfxFile.Close()

		pfxBytes, err := io.ReadAll(pfxFile)
		if err != nil {
			slog.Error("rootca POST: read pfx file", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if len(pfxBytes) == 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "A 'pfx' file is required."})
			return
		}

		password := r.FormValue("password")

		cert, _, err := rootca.ParsePKCS12(pfxBytes, password)
		if err != nil {
			// pkcs12.Decode fails when there is no private key ("pkcs12: private key
			// missing"), so this covers both a bad-password and a public-cert-only PFX.
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "Could not parse the uploaded file as a PFX with the given password.",
			})
			return
		}

		// BasicConstraints CA:true check — mirrors C# X509BasicConstraintsExtension check.
		if !cert.IsCA {
			writeJSON(w, http.StatusBadRequest, errorResponse{
				Error: "The uploaded certificate is not a CA (X509BasicConstraintsExtension.CertificateAuthority is false). Did you upload a leaf cert instead of a CA?",
			})
			return
		}

		row, err := store.Save(pfxBytes, password, cert)
		if err != nil {
			slog.Error("rootca POST: save", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Clear the mint cache so any previously-minted leaves signed by the old CA are
		// discarded. Mirrors C# minter.ClearCache() call after store.Invalidate().
		minter.ClearCache()

		writeJSON(w, http.StatusOK, toRootCaResponse(row))
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handleDeleteRootCa handles DELETE /api/admin/rootca (JWT-protected).
// Removes the stored CA from the DB and clears both caches. Returns 204 No Content.
// Mirrors C# AdminRootCaEndpoints.MapDelete("").
func handleDeleteRootCa(store *rootca.Store, minter *rootca.Minter, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(); err != nil {
			slog.Error("rootca DELETE: delete", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		minter.ClearCache()
		w.WriteHeader(http.StatusNoContent)
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}

// handleGetRootCaCertificate handles GET /api/admin/rootca/certificate (JWT-protected).
// Returns the CA's public certificate as a DER-encoded binary download
// (application/x-x509-ca-cert). Returns 404 if no CA is configured.
// Mirrors C# AdminRootCaEndpoints.MapGet("/certificate").
func handleGetRootCaCertificate(store *rootca.Store, authSvc *auth.AdminAuthService) http.HandlerFunc {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		derBytes, err := store.GetCertDER()
		if err != nil {
			slog.Error("rootca GET /certificate: get DER", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if derBytes == nil {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "No root CA is currently configured."})
			return
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="rbi-root-ca.cer"`)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(derBytes); err != nil {
			slog.Error("rootca GET /certificate: write response", "err", err)
		}
	})
	return auth.RequireJWT(authSvc, inner).ServeHTTP
}
