# Go Promo Code Service

A Go HTTP service that manages promo code distribution using Firestore and sends emails via Mandrill.

## Prerequisites

- Go 1.24.0 or later
- Google Cloud Project with Firestore enabled
- Mandrill API key (from Mailchimp)
- Docker (optional, for containerized deployment)

## Environment Variables

The following environment variables are required:

- `GOOGLE_CLOUD_PROJECT` - Your Google Cloud Project ID
- `MANDRILL_API_KEY` - Your Mandrill API key
- `SECURITY_TOKEN` - Shared secret key for form validation
- `FROM_EMAIL` - Email address to send from (e.g., "no-reply@jordanraynor.com")
- `PORT` - Server port (defaults to 8080 if not set)

### Google Cloud Authentication

The Firestore client requires Google Cloud credentials. You have two options:

**Option 1: Service Account Key (Recommended for production)**
```bash
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/your/service-account-key.json"
```

**Option 2: Application Default Credentials (For local development)**
```bash
gcloud auth application-default login
```

If credentials are not set up, the application will hang or fail with an authentication error.

## Local Development

### Option 1: Run directly with Go

1. Install dependencies:
   ```bash
   go mod download
   ```

2. Set environment variables:
   ```bash
   export GOOGLE_CLOUD_PROJECT="your-project-id"
   export MANDRILL_API_KEY="your-mandrill-key"
   export SECURITY_TOKEN="your-secret-token"
   export FROM_EMAIL="no-reply@jordanraynor.com"
   export PORT="8080"
   ```

3. Run the application:
   ```bash
   go run main.go
   ```

### Option 2: Build and run binary

1. Build the application:
   ```bash
   go build -o server main.go
   ```

2. Set environment variables (same as above)

3. Run the binary:
   ```bash
   ./server
   ```

## Docker Build and Run

### Local Docker Build

1. Build the Docker image:
   ```bash
   docker build -t go-promo-code .
   ```

2. Run the container with environment variables:
   ```bash
   docker run -p 8080:8080 \
     -e GOOGLE_CLOUD_PROJECT="your-project-id" \
     -e MANDRILL_API_KEY="your-mandrill-key" \
     -e SECURITY_TOKEN="your-secret-token" \
     -e FROM_EMAIL="no-reply@jordanraynor.com" \
     -e PORT="8080" \
     go-promo-code
   ```

   Or use a `.env` file:
   ```bash
   docker run -p 8080:8080 --env-file .env go-promo-code
   ```

## Google Cloud Run Deployment

### Step 1: Build the Docker image with Google Cloud Build

Build the image in Google Cloud Build (this pushes it to Google Container Registry or Artifact Registry):

```bash
gcloud builds submit --tag gcr.io/YOUR_PROJECT_ID/go-promo-code
```

Or if using Artifact Registry:
```bash
gcloud builds submit --tag REGION-docker.pkg.dev/YOUR_PROJECT_ID/REPOSITORY_NAME/go-promo-code
```

Replace:
- `YOUR_PROJECT_ID` with your Google Cloud project ID
- `REGION` with your region (e.g., `us-central1`)
- `REPOSITORY_NAME` with your Artifact Registry repository name

### Step 2: Deploy to Cloud Run

Deploy the container to Cloud Run:

```bash
gcloud run deploy go-promo-code \
  --image gcr.io/YOUR_PROJECT_ID/go-promo-code \
  --platform managed \
  --region REGION \
  --allow-unauthenticated \
  --set-env-vars GOOGLE_CLOUD_PROJECT=YOUR_PROJECT_ID \
  --set-env-vars MANDRILL_API_KEY=your-mandrill-key \
  --set-env-vars SECURITY_TOKEN=your-secret-token \
  --set-env-vars FROM_EMAIL=no-reply@jordanraynor.com \
  --set-env-vars PORT=8080
```

Or deploy and let Cloud Run build it automatically (combines build + deploy):

```bash
gcloud run deploy go-promo-code \
  --source . \
  --platform managed \
  --region REGION \
  --allow-unauthenticated \
  --set-env-vars GOOGLE_CLOUD_PROJECT=YOUR_PROJECT_ID \
  --set-env-vars MANDRILL_API_KEY=your-mandrill-key \
  --set-env-vars SECURITY_TOKEN=your-secret-token \
  --set-env-vars FROM_EMAIL=no-reply@jordanraynor.com
```

**Note**: The `--source .` flag will automatically build the Docker image from the Dockerfile in the current directory.

## API Endpoints

### GET /health

Health check endpoint for Cloud Run readiness checks.

**Response:**
- `200 OK` - Service is ready
- `503 Service Unavailable` - Service is still initializing

### POST /submit

Submit a promo code request.

**Form Data:**
- `email` (required) - User's email address
- `secret_key` (required) - Must match `SECURITY_TOKEN` environment variable

**Example:**
```bash
curl -X POST http://localhost:8080/submit \
  -d "email=user@example.com" \
  -d "secret_key=your-secret-token"
```

**Response:**
- `200 OK` - Success with promo code message
- `400 Bad Request` - Missing email or invalid form data
- `401 Unauthorized` - Invalid security token
- `503 Service Unavailable` - Service is still initializing (Firestore not ready)
- `500 Internal Server Error` - No promo codes available or transaction failed

## Firestore Setup

The service expects a Firestore collection named `promo-codes` with documents containing:
- `active` (boolean) - Whether the promo code is available
- `email` (string) - Email of the user who claimed it (empty string if unclaimed)
- `discount_code` (string) - The actual promo code

## Troubleshooting

### Application hangs on startup

If `go run main.go` hangs, it's likely because the Firestore client cannot authenticate with Google Cloud. The application now includes a 30-second timeout and debug logging to help identify the issue.

**Solution:**
1. Set up Google Cloud authentication (see "Google Cloud Authentication" above)
2. Verify your credentials work:
   ```bash
   gcloud auth list
   ```
3. Check the debug output - the application will log where it's looking for credentials

### Authentication errors

If you see errors like "Failed to create firestore client", ensure:
- Your Google Cloud project has Firestore enabled
- Your credentials have the necessary Firestore permissions
- The `GOOGLE_CLOUD_PROJECT` environment variable matches your actual project ID

### Cloud Run deployment issues

The application is optimized for Cloud Run:
- HTTP server starts immediately (doesn't wait for Firestore initialization)
- Firestore client initializes asynchronously in the background
- `/health` endpoint returns 503 until Firestore is ready
- `/submit` endpoint returns 503 if called before Firestore is ready

If deployment still fails, check:
- All required environment variables are set in Cloud Run
- The service account has Firestore permissions
- Check Cloud Run logs for initialization errors

