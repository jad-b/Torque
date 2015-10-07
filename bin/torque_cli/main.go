package main

/* torque_cli

Example usage:

	# Post a bodyweight through the Torque REST API
	torque_cli -addr http://localhost:18000 create bodyweight -userid -weight 181.2 -comment "a.m."
*/

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/jad-b/flagit"
	"github.com/jad-b/torque"
	"github.com/jad-b/torque/client"
	"github.com/jad-b/torque/metrics"
	"github.com/jmoiron/sqlx"
)

// A CommandLineActor can talk to DB's or Web servers
type CommandLineActor interface {
	torque.DBActor
	torque.RESTfulHandler
}

var (
	tAPI     *client.TorqueAPI
	registry = map[string]CommandLineActor{
		"bodyweight": &metrics.Bodyweight{},
	}
	web      = flag.Bool("web", false, "Act against the Torque API server")
	addr     = flag.String("addr", "http://localhost:18000", "Host:port of Torque server")
	verbose  = flag.Bool("v", false, "Toggle verbose output")
	username = flag.String("username", "", "Username for account")
	password = flag.String("password", "", "Password for account")
	// The error that killed the program. Having this as a script-global allows
	// us to set an error wherever, recover generically with a 'defer' in
	// main(), and still output something meaningful.
	terminalError error
)

/* cli is the command-line interface for Torque.

Command syntax:
	torque <options> <action> <resource> arguments>
*/
func main() {
	// Handle all errors generically
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Torque: %s\n\t'%s'", terminalError.Error(), strings.Join(os.Args, " "))
		}
	}()

	// Get the first pass of flags out of the way
	flag.Parse()
	// pass the remaining args off to the resources to handle
	err := handleSubcommand()
	if err != nil {
		log.Printf("Torque: %s\n\t'%s'", err.Error(), strings.Join(os.Args, " "))
	}
}

// handleSubcommand delegtes sub-command flags appropriately.
//
// Example Usage:
//	   torque_cli create bodyweight -weight 182.3 -comment "Feeling tremendous."
func handleSubcommand() (err error) {
	remainder := flag.Args()
	lenRemainder := len(remainder)
	// Check we received a minimal amount of arguments
	if lenRemainder < 1 {
		return errors.New("No action specified")
	} else if lenRemainder < 2 {
		return errors.New("No resource specified")
	} else if lenRemainder < 3 {
		return errors.New("No data was provided")
	}
	log.Print("Remaining flags: ", remainder)

	// Delegate remaining arg parsing to the identified resource
	action, resource := remainder[0], remainder[1]
	r, ok := registry[resource]
	if !ok {
		return fmt.Errorf("%s not recognized as resource", remainder[1])
	}
	// Resource located; have it parse the remaining flags.
	fs := flagit.FlagIt(r)
	err = fs.Parse(remainder[2:])
	if err != nil {
		return err
	}
	// Determine if we're going over HTTP or directly to the database
	if *web { // Operate over REST API
		_, err = handleWebAction(r, action)
		return err
	}
	return handleDBAction(r, action)
}

func handleWebAction(res torque.RESTfulResource, action string) (resp *http.Response, err error) {
	// Authenticate to torque server
	tAPI = client.NewTorqueAPI(*addr)
	err = tAPI.Authenticate(*username, *password)
	if err != nil {
		return nil, err
	}
	resp, err = ActOnWeb(res, action)
	if *verbose && resp != nil {
		torque.LogResponse(resp)
	}
	return resp, err
}

// ActOnWeb sends an API request to Torque API web server.
func ActOnWeb(rh torque.RESTfulResource, action string) (*http.Response, error) {
	log.Printf("HTTP operation on %#v", rh)
	switch action {
	case "create":
		return tAPI.Post(rh)
	case "retrieve":
		return tAPI.Get(rh, nil)
	case "update":
		return tAPI.Put(rh)
	case "delete":
		return tAPI.Delete(rh)
	default:
		return nil, fmt.Errorf("%s is an invalid action", action)
	}
}

func handleDBAction(res torque.DBActor, action string) error {
	// Open up a Database connection
	pgconf := torque.LoadPostgresConfig(*torque.PsqlConf)
	db := torque.OpenDBConnection(pgconf)
	return ActOnDB(res, action, db)
}

// ActOnDB requests the actor perform it's correct method against the database.
func ActOnDB(actor torque.DBActor, action string, db *sqlx.DB) error {
	log.Printf("DB operation on %#v", actor)
	switch action {
	case "create":
		return actor.Create(db)
	case "retrieve":
		return actor.Retrieve(db)
	case "update":
		return actor.Update(db)
	case "delete":
		return actor.Delete(db)
	default:
		return fmt.Errorf("%s is an invalid action", action)
	}
}
