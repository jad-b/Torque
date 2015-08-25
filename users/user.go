package users

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/jad-b/torque"
)

// UserAuthSQL is SQL for creating the auth.UserAuths table
const (
	UserAuthSQL = `
CREATE TABLE auth.UserAuths(
  "id" integer PRIMARY KEY,
  "username" text NOT NULL UNIQUE,
  "account_created" timestamp(0) with time zone NOT NULL,
  "enabled" boolean DEFAULT TRUE,
  "superuser" boolean DEFAULT FALSE,
  "password_hash" text NOT NULL,
  "password_salt" text NOT NULL,
  "cost" integer NOT NULL,
  "current_token" text,
  "token_created" timestamp(0) with time zone,
  "token_last_used" timestamp(0) with time zone
);`
)

// UserAuth holds the auth token data mapped to a UserAuth ID.
// TODO It also holds some data, like Enabled, AccountCreated, that might be better
// served in a UserMeta table, but I left it here for simplicity's sake. Also, I
// hear JOINs are expensive.
type UserAuth struct {
	// Primary key. Links user tables together
	ID int `json:"id"`
	// Keep username close to passwordHash for authentication calls
	Username       string    `json:"username"`
	AccountCreated time.Time `json:"account_created"`
	Enabled        bool      `json:"enabled"`
	Superuser      bool      `json:"superuser"`
	// Salt used to hash the password
	PasswordSalt string
	// Type of hash used
	PasswordHash string
	// Power-of-two times we iterated over the stored password when hashing
	Cost int
	// Currently active auth token
	CurrentToken string    `json:"token"`
	TokenCreated time.Time `json:"timestamp"`
	// Last time the token was used in an API request
	TokenLastUsed time.Time `json:"token_last_used"`
}

// Authenticate logs the User in on the Torque Server.
// This is a client-side call.
// This has the side-effect of modifying the calling object's state.
func Authenticate(serverURL, username, password string) UserAuth {
	// Prepare the URL
	u, err := url.Parse(serverURL)
	if err != nil {
		log.Fatal(err)
	}
	u.Path = torque.SlashJoin(u.Path, "users", "authenticate")
	// Prepare the JSON body
	body, err := json.Marshal(u)
	if err != nil {
		log.Fatal("Failed to marshal credentials")
	}
	// Send the auth request
	resp, err := http.Post(u.String(), "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Fatal("Failed to authenticate")
	}
	// Parse the response into a User object
	user := &UserAuth{}
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("Failed to read authentication response body")
	}
	err = json.Unmarshal(respBody, user)
	if err != nil {
		log.Fatal("Failed to unmarshal authentication response body")
	}
	return *user
}

// IsAuthenticated returns whether or not the User is authenticated. This
// method *does not* touch the server, so it relies upon available client-side
// data.
func (u *UserAuth) IsAuthenticated() bool {
	return u.CurrentToken != "" &&
		time.Since(u.TokenCreated) < AuthTokenLifespan
}

// NewUserAuth creates a new UserAuth instance with some defaults in place.
func NewUserAuth() *UserAuth {
	return &UserAuth{
		PasswordSalt:   NewSalt(DefaultSaltLength),
		Cost:           DefaultBcryptCost,
		AccountCreated: time.Now(),
	}
}

// ValidatePassword verifies if the given username/password is valid.
func (u *UserAuth) ValidatePassword(password string) bool {
	// Hash the password
	hashed, _, _ := DefaultHash(password)
	u.Retrieve(torque.DBConn) // Lookup from the database
	return hashed == u.PasswordHash
}

// ValidateAuthToken verifies the given auth token is valid for the user.
func (u *UserAuth) ValidateAuthToken(token string) bool {
	u.Retrieve(torque.DBConn) // Lookup from the database
	return token == u.CurrentToken
}

/*
	DBActor
*/

// Create inserts a new UserAuth row into the database
func (u *UserAuth) Create(conn *sql.DB) error {
	_, err := conn.Exec(`
	INSERT INTO auth.UserAuth (
		id,
		username,
		account_created,
		enabled,
		superuser,
		password_hash,
		password_salt,
		cost,
		current_token,
		token_created,
		token_last_used)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		u.ID,
		u.Username,
		u.AccountCreated,
		u.Enabled,
		u.Superuser,
		u.PasswordHash,
		u.PasswordSalt,
		u.Cost,
		u.CurrentToken,
		u.TokenCreated,
		u.TokenLastUsed)
	if err != nil {
		return err
	}
	return nil
}

// Retrieve a UserAuth from the DB by filtering on Username
func (u *UserAuth) Retrieve(conn *sql.DB) error {
	err := conn.QueryRow(`
	SELECT (
		id,
		username,
		account_created,
		enabled,
		superuser,
		password_hash,
		password_salt,
		cost,
		current_token,
		token_created,
		token_last_used)
	FROM auth.UserAuth
	WHERE username=$1`,
		u.Username).Scan(u)
	if err != nil {
		return err
	}
	return nil
}

// Update a user entry in the database
// TODO(jdb) Might be a bad idea to override everything - kind of implies you'l
// want to RETRIEVE the existing record, apply changes, then UPDATE the row.
// Or maybe we should implement PATCH for partial updates.
func (u *UserAuth) Update(conn *sql.DB) error {
	_, err := conn.Exec(`
	UPDATE auth.UserAuth
	SET account_created='$2',
		enabled='$3',
		superuser='$4',
		password_hash='$5',
		password_salt='$6',
		cost='$7',
		current_token='$8',
		token_created='$9',
		token_last_used='$10')
	WHERE username=$1`,
		u.Username,
		u.AccountCreated,
		u.Enabled,
		u.Superuser,
		u.PasswordHash,
		u.PasswordSalt,
		u.Cost,
		u.CurrentToken,
		u.TokenCreated,
		u.TokenLastUsed)
	if err != nil {
		return err
	}
	return nil
}

// Delete removes a UserAuth record from the database. In most cases it will
// probably be best practice to simply flag a user as disabled via a PUT, but
// we do also need to expose this ability.
func (u *UserAuth) Delete(conn *sql.DB) error {
	err := conn.QueryRow(`
	DELETE FROM auth.UserAuth
	WHERE username=$1`, u.Username).Scan(u)
	if err != nil {
		return err
	}
	return nil
}

/*
	RESTfulHandler
*/

// HandlePost creates a new UserAuth record.
func (u *UserAuth) HandlePost(w http.ResponseWriter, req *http.Request) {
	userID, err := parseUserID(req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.ID = userID
	err = torque.ReadBodyTo(w, req, u)
	if err != nil {
		http.Error(w, "Failed to parse JSON from request", http.StatusBadRequest)
		return
	}
	if err = u.Create(torque.DBConn); err != nil {
		http.Error(w, "Failed to write record to database", http.StatusInternalServerError)
		return
	}
	log.Printf("Created %+v", u)
	torque.WriteOkayJSON(w, u)
}

// HandleGet returns the specified UserAuth record
func (u *UserAuth) HandleGet(w http.ResponseWriter, req *http.Request) {
	userID, err := parseUserID(req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.ID = userID
	if err = u.Retrieve(torque.DBConn); err != nil {
		http.NotFound(w, req)
		return
	}
	torque.WriteOkayJSON(w, u)
}

// HandlePut updates a UserAuth resource.
func (u *UserAuth) HandlePut(w http.ResponseWriter, req *http.Request) {
	userID, err := parseUserID(req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.ID = userID
	// Parse body of PUT request into a UserAuth struct
	err = torque.ReadBodyTo(w, req, u)
	if err != nil {
		http.Error(w, "Failed to parse JSON from request", http.StatusBadRequest)
		return
	}
	if err = u.Update(torque.DBConn); err != nil {
		http.Error(w, "Failed to write record to database", http.StatusInternalServerError)
		return
	}
	// Write updated record to client
	torque.WriteOkayJSON(w, u)
}

// HandleDelete removes a user record from the database.
func (u *UserAuth) HandleDelete(w http.ResponseWriter, req *http.Request) {
	userID, err := parseUserID(req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.ID = userID
	if err = u.Delete(torque.DBConn); err != nil {
		http.NotFound(w, req)
		return
	}
	torque.WriteOkayJSON(w, u)
}

func parseUserID(earl *url.URL) (int, error) {
	// Parse user ID from URL
	re := regexp.MustCompile(`/users/(\d+)/?`)
	parts := re.FindStringSubmatch(earl.Path)
	if len(parts) < 2 {
		return 0, fmt.Errorf("Expected /users/{user_id}/, not %s", earl.Path)
	}
	userID, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("%s is an invalid user ID", parts[1])
	}
	return userID, nil
}

/*
	RESTfulResource
*/

// GetResourceName returns the name UserAuth wishes to be referred to by in the
// URL
func (u *UserAuth) GetResourceName() string {
	return torque.SlashJoin("users", u.Username)
}
