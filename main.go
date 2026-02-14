package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

type Config struct {
	Port          string
	ProjectID     string
	Collection    string
	MailchimpKey  string // Mandrill API Key
	SecurityToken string // Shared secret key for form validation
	FromEmail     string
}

// Global Firestore Client
var (
	client     *firestore.Client
	clientMu   sync.RWMutex
	clientInit sync.Once
	initErr    error
)

// Mandrill API Structures
type MandrillMessage struct {
	Subject string              `json:"subject"`
	Html    string              `json:"html"`
	From    string              `json:"from_email"`
	To      []MandrillRecipient `json:"to"`
}
type MandrillRecipient struct {
	Email string `json:"email"`
	Type  string `json:"type"` // "to"
}
type MandrillPayload struct {
	Key     string          `json:"key"`
	Message MandrillMessage `json:"message"`
}

func main() {
	ctx := context.Background()

	// Log startup
	log.Printf("Starting application...")
	log.Printf("PORT: %s", os.Getenv("PORT"))
	log.Printf("GOOGLE_CLOUD_PROJECT: %s", os.Getenv("GOOGLE_CLOUD_PROJECT"))

	// 1. Configuration
	cfg := Config{
		Port:          getEnv("PORT", "8080"),
		ProjectID:     getEnv("GOOGLE_CLOUD_PROJECT", ""),
		Collection:    "promo-codes",
		MailchimpKey:  os.Getenv("MANDRILL_API_KEY"),
		SecurityToken: os.Getenv("SECURITY_TOKEN"),
		FromEmail:     os.Getenv("FROM_EMAIL"), // e.g., "no-reply@jordanraynor.com"
	}

	// Validate config but don't exit - let server start and return errors in handlers
	missingVars := []string{}
	if cfg.ProjectID == "" {
		missingVars = append(missingVars, "GOOGLE_CLOUD_PROJECT")
	}
	if cfg.MailchimpKey == "" {
		missingVars = append(missingVars, "MANDRILL_API_KEY")
	}
	if cfg.SecurityToken == "" {
		missingVars = append(missingVars, "SECURITY_TOKEN")
	}
	if cfg.FromEmail == "" {
		missingVars = append(missingVars, "FROM_EMAIL")
	}
	if len(missingVars) > 0 {
		log.Printf("WARNING: Missing environment variables: %v", missingVars)
		log.Printf("Service will start but /submit endpoint will not work until variables are set")
	}

	// 2. Initialize Firestore Client asynchronously (don't block server startup)
	if cfg.ProjectID != "" {
		go initFirestoreClient(ctx, cfg.ProjectID)
	} else {
		log.Printf("Skipping Firestore initialization - GOOGLE_CLOUD_PROJECT not set")
	}

	// 3. Root endpoint (responds immediately for Cloud Run health checks)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Service is running"))
	})

	// 4. Health check endpoint (for Cloud Run readiness)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		clientMu.RLock()
		ready := client != nil && initErr == nil
		clientMu.RUnlock()

		if ready {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Initializing..."))
		}
	})

	// 5. HTTP Handler
	http.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}

		// --- SECURITY CHECK ---
		if r.FormValue("secret_key") != cfg.SecurityToken {
			// This is a common method for simple shared-secret validation.
			// For high-security apps, look into HMAC or OAuth.
			http.Error(w, "Invalid security token", http.StatusUnauthorized)
			return
		}
		// ----------------------

		email := r.FormValue("email")
		if email == "" {
			http.Error(w, "Email required", http.StatusBadRequest)
			return
		}

		// Check if Firestore client is ready
		clientMu.RLock()
		fsClient := client
		fsErr := initErr
		clientMu.RUnlock()

		if fsClient == nil || fsErr != nil {
			if fsErr != nil {
				log.Printf("Firestore not ready: %v", fsErr)
			}
			http.Error(w, "Service initializing, please try again in a moment", http.StatusServiceUnavailable)
			return
		}

		// Run the atomic transaction
		code, err := claimPromo(ctx, cfg.Collection, email)
		if err != nil {
			log.Printf("Transaction failed: %v", err)
			http.Error(w, "Error claiming promo (maybe none left?)", http.StatusInternalServerError)
			return
		}

		// Send Email (Async) using Mailchimp/Mandrill
		go func() {
			if err := sendMandrillEmail(cfg, email, code); err != nil {
				log.Printf("Failed to send email via Mandrill: %v", err)
			}
		}()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf("Success! Code %s reserved for %s", code, email)))
	})

	// 6. Start HTTP server immediately (required for Cloud Run)
	listenAddr := fmt.Sprintf("0.0.0.0:%s", cfg.Port)
	log.Printf("HTTP server starting on %s", listenAddr)
	log.Printf("Server is ready to accept connections")

	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

// initFirestoreClient initializes the Firestore client in the background
func initFirestoreClient(ctx context.Context, projectID string) {
	clientInit.Do(func() {
		log.Printf("Initializing Firestore client for project: %s", projectID)
		log.Printf("Looking for Google Cloud credentials...")
		log.Printf("GOOGLE_APPLICATION_CREDENTIALS: %s", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))

		// Use a timeout context to prevent hanging indefinitely
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		fsClient, err := firestore.NewClient(initCtx, projectID)

		clientMu.Lock()
		client = fsClient
		initErr = err
		clientMu.Unlock()

		if err != nil {
			log.Printf("Failed to create firestore client: %v", err)
		} else {
			log.Printf("Firestore client initialized successfully")
		}
	})
}

// claimPromo logic is unchanged from the Firestore solution (atomic update)
func claimPromo(ctx context.Context, collection, userEmail string) (string, error) {
	clientMu.RLock()
	fsClient := client
	clientMu.RUnlock()

	if fsClient == nil {
		return "", fmt.Errorf("firestore client not initialized")
	}

	var code string

	err := fsClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// 1. Find the first available promo
		iter := fsClient.Collection(collection).
			Where("active", "==", true).
			Where("email", "==", "").
			Limit(1).
			Documents(ctx)

		doc, err := iter.Next()
		if err == iterator.Done {
			return fmt.Errorf("no promos available")
		}
		if err != nil {
			return err
		}

		// 2. Transactional Read (to ensure the document hasn't changed since the query)
		docRef := fsClient.Collection(collection).Doc(doc.Ref.ID)
		snap, err := tx.Get(docRef)
		if err != nil {
			return err
		}

		// 3. Verify availability and get code
		if active, _ := snap.DataAt("active"); active != true {
			return fmt.Errorf("promo claimed by someone else, retry")
		}

		val, _ := snap.DataAt("discount_code")
		code = fmt.Sprint(val)

		// 4. Transactional Write: Claim the promo
		return tx.Update(docRef, []firestore.Update{
			{Path: "active", Value: false},
			{Path: "email", Value: userEmail},
		})
	})

	return code, err
}

func sendMandrillEmail(cfg Config, toEmail, code string) error {
	// 1. Construct the HTML email body
	htmlBody := fmt.Sprintf(`
		<html>
			<body>
				<h1>Your Exclusive Code!</h1>
				<p>Thank you for signing up. Please use your unique code below:</p>
				<h2 style="color:#007bff; font-weight:bold;">%s</h2>
				<p>This code is reserved just for you.</p>
			</body>
		</html>`, code)

	// 2. Construct the Mandrill API Payload
	payload := MandrillPayload{
		Key: cfg.MailchimpKey,
		Message: MandrillMessage{
			Subject: "Your Promo Code is Here!",
			Html:    htmlBody,
			From:    cfg.FromEmail,
			To: []MandrillRecipient{
				{Email: toEmail, Type: "to"},
			},
		},
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON payload: %w", err)
	}

	// 3. Send the request to Mandrill
	mandrillURL := "https://mandrillapp.com/api/1.0/messages/send.json"
	resp, err := http.Post(mandrillURL, "application/json", bytes.NewBuffer(payloadJSON))
	if err != nil {
		return fmt.Errorf("mandrill API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		return fmt.Errorf("mandrill API returned status %d. Error: %v", resp.StatusCode, result)
	}

	return nil
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
