package imqsauth

import (
	"bytes"
	"testing"
	"golang.org/x/net/websocket"
	"strings"
	"net/http"
	"fmt"
	"time"
	"encoding/base64"
	"net/url"
	"errors"
)

const origin = "http://localhost/"
const originHttpUrl = "http://localhost:3377"
var failed = ""

// This might be the appropriate place for REST API tests
// At present, REST tests performed in the ruby script resttest.rb
// The WEB SOCKET tests are "go test" commands called from resttest.rb and execute the test code in this file


func TestWebSocket(t *testing.T){
	const wsUrl = "ws://localhost:3377/authnotifications"

	const normalMessageReceived = 0
	const unexpectedError = 1
	const httpCallError = 2
	const validErrorReceived = 3
	const expectedErrorNotReceived = 4

	var receiveQueue = make(chan int)
	var receivedOnValidSocket = false
	var receivedOnErrorSocket = false

	// This test assumes auth was freshly started up with "TESTCONFIG" from the resttest.rb script

	cookie, err := login()
	if (cookie == "") {
		t.Fatal("Login failed: no cookie\n")
	}

	if (err != nil) {
		t.Fatalf("Login failed: %#+v\n", err)
	}

	//websocket with auth
	ws, err := createWebSocketAndConnect(wsUrl, origin, cookie)
	if (err != nil) {
		t.Fatal("Error in handshake valid auth connection.\n")
	}

	//websocket without auth
	wsFail, err := createWebSocketAndConnect(wsUrl, origin, "")
	if err != nil {
		t.Fatal("Error in handshake invalid auth connection: %v\n", err)
	}

	var msg = make([]byte, 512)
	var msgError = make([]byte, 512)
	var n int
	const timeout = 5
	var counter = 0

	// enable ticker used for timeout
	tick := time.Tick(1000 * time.Millisecond)

	go func() {
		// wait for message from service, should be valid service
		// t.Fatal does not work inside goroutines, post error on channel to retrieve the error message
		if n, err = ws.Read(msg); err != nil {
			failed = fmt.Sprintf("Error receiving message: %v\n", err)
			receiveQueue <- unexpectedError
		} else {
			receiveQueue <- normalMessageReceived
		}
	}()

	go func() {
		// wait for message from service, should be EOF
		// t.Fatal does not work inside goroutines, post error on channel to retrieve the error message
		if n, err = wsFail.Read(msgError); err != nil {
			//we expect an error here
			receiveQueue <- validErrorReceived
		} else {
			//did not receive an error
			failed = fmt.Sprint("EOF expected but not received (invalid auth test)\n")
			receiveQueue <- expectedErrorNotReceived
		}
	}()

	go func() {
		// perform new user and group rest calls
		client := &http.Client{}

		// create user
		r1, err := createDummyPUTRequest(originHttpUrl + "/create_user?identity=socketuser&password=socketuser", cookie)

		if hasRequestErrors(err, nil, 0, "Fail: ") {
			receiveQueue <- httpCallError
			return
		}

		resp, err := client.Do(r1)
		if hasRequestErrors(err, resp, 200, "Error adding user: ") {
			receiveQueue <- httpCallError
			return
		}

		// create group
		r2, err := createDummyPUTRequest(originHttpUrl + "/create_group?groupname=socketusergroup", cookie)
		if hasRequestErrors(err, nil, 0, "Fail: ") {
			receiveQueue <- httpCallError
			return
		}

		resp, err = client.Do(r2)
		if hasRequestErrors(err, resp, 200, "Error adding group:") {
			receiveQueue <- httpCallError
			return
		}

		// add user to group
		r3, err := createDummyPOSTRequest(originHttpUrl + "/set_user_groups?identity=socketuser&groups=enabled,socketusergroup", cookie)
		if hasRequestErrors(err, nil, 0, "Fail: ") {
			receiveQueue <- httpCallError
			return
		}

		resp, err = client.Do(r3)
		if hasRequestErrors(err, resp, 200, "Error adding group to user: ") {
			receiveQueue <- httpCallError
			return
		}
	}()

	for {
		select {
		case res := <-receiveQueue:
			if res == normalMessageReceived {
				txt := string(msg[:n])
				i := strings.Index(txt, "auth:logout")
				if (i != 0) {
					t.Fatalf("Invalid message received.\n")
				}
				i = strings.Index(txt, "socketuser")
				if (i < 0) {
					t.Fatalf("Could not find user identity in message\n")
				}

				// success, set flag
				receivedOnValidSocket = true
			} else if res == unexpectedError {
				t.Fatalf(failed)
			} else if res == httpCallError {
				t.Fatalf(failed)
			} else if res == expectedErrorNotReceived {
				t.Fatalf(failed)
			} else if res == validErrorReceived {
				// success, set flag
				receivedOnErrorSocket = true
			}
		case <-tick:
			counter = counter + 1
			if counter >= timeout {
				t.Fatalf("Timeout\n")
			}
		default:
			if receivedOnErrorSocket && receivedOnValidSocket {
				t.Logf("All conditions satisfied...quit\n")
				return
			}
		}
	}
	if len(failed) > 0 {
		t.Fatal(failed)
	}
}

func createDummyPUTRequest(url string, cookie string) (*http.Request, error) {
	return createDummyDataRequest(url, "PUT", cookie)
}

func createDummyPOSTRequest(url string, cookie string) (*http.Request, error) {
	return createDummyDataRequest(url, "POST", cookie)
}

func createDummyDataRequest(url string, verb string, cookie string) (*http.Request, error) {
	var jsonStr = []byte(``)
	r, err := http.NewRequest(verb, url, bytes.NewBuffer(jsonStr))
	if err != nil {
		return nil, err
	}
	r.Header.Add("Cookie", cookie)
	return r, nil
}

func login() (string, error) {
	var jsonStr = []byte(``)
	r, err := http.NewRequest("POST", originHttpUrl + "/login", bytes.NewBuffer(jsonStr) )
	if err != nil {
		return "", err
	}

	r.Header.Add("Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:ADMIN")) )
	client := &http.Client{}
	resp, err := client.Do(r);
	if err != nil {
		return "", err
	}
	if resp.StatusCode == 200 {
		return resp.Header.Get("Set-Cookie"), nil
	} else {
		return "", nil
	}
}

func hasRequestErrors(err error, resp *http.Response, expectedStatusCode int, message string) (bool) {
	// Handles both request construction and request failures
	if (err != nil) {
		failed = fmt.Sprintf(message + "%v\n", err.Error())
		return true
	}
	if (resp != nil) && (resp.StatusCode != expectedStatusCode) {
		failed = fmt.Sprintf(message + "%#+v\n", resp)
		return true
	}
	return false
}

func createWebSocketAndConnect(wsUrlLocal string, wsOriginLocal string, cookieLocal string) (*websocket.Conn, error){
	wsUrlObject, err := url.Parse(wsUrlLocal)
	if (err != nil) {
		return nil, errors.New(fmt.Sprintf("Could not construct ws url: %v\n", err))
	}

	originUrlObject, err := url.Parse(wsOriginLocal)
	if (err != nil) {
		return nil, errors.New(fmt.Sprintf("Could not construct origin url: %v\n", err))
	}

	headers := http.Header{}
	headers.Add("Cookie", cookieLocal)

	config := websocket.Config{Location:wsUrlObject, Origin:originUrlObject, Protocol:nil, Version:websocket.ProtocolVersionHybi13, TlsConfig:nil, Header:headers}
	ws, err := websocket.DialConfig(&config)
	return ws, err
}