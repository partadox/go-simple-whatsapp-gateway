package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

// ClientStatus represents the connection status of a WhatsApp client
type ClientStatus string

const (
	StatusLoggedOut ClientStatus = "logged_out"
	StatusConnected ClientStatus = "connected"
	StatusDisconnected ClientStatus = "disconnected"
	StatusError ClientStatus = "error"
)

// ClientState represents the persistent state of a client
type ClientState struct {
	ID               string       `json:"id"`
	Status           ClientStatus `json:"status"`
	LastActivity     time.Time    `json:"last_activity"`
	Connected        bool         `json:"connected"`
	LoggedIn         bool         `json:"logged_in"`
	PushName         string       `json:"push_name"`
	PhoneNumber      string       `json:"phone_number,omitempty"`
	ConnectionError  string       `json:"connection_error,omitempty"`
}

// Client represents a WhatsApp client instance
type Client struct {
	ID           string
	client       *whatsmeow.Client
	container    *sqlstore.Container
	eventHandler func(event interface{})
	deviceStore  *store.Device
	
	// Client state
	status      ClientStatus
	lastActivity time.Time
	connError   string
	
	// For safe concurrent access
	mutex       sync.RWMutex
	
	// For QR channel
	qrChan      chan string
	qrTimeout   *time.Timer
	
	// For phone pairing channel
	pairChan    chan string
	pairTimeout *time.Timer
	
	// Data directory
	dataDir     string
}

// NewClient creates a new WhatsApp client
func NewClient(id string, dataDir string) (*Client, error) {
	if id == "" {
		return nil, errors.New("client ID cannot be empty")
	}

	// Create client directory
	clientDir := filepath.Join(dataDir, id)
	if err := os.MkdirAll(clientDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create client directory: %w", err)
	}

	// Create database file
	dbPath := filepath.Join(clientDir, "whatsapp.db")
	container, err := sqlstore.New("sqlite3", "file:"+dbPath+"?_foreign_keys=on", waLog.Stdout("sqlstore", "DEBUG", true))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Get device store
	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	// Create the client
	wac := whatsmeow.NewClient(deviceStore, waLog.Stdout("whatsapp", "INFO", true))

	// Create the client wrapper
	c := &Client{
		ID:          id,
		client:      wac,
		container:   container,
		deviceStore: deviceStore,
		status:      StatusLoggedOut,
		lastActivity: time.Now(),
		dataDir:     clientDir,
		qrChan:      make(chan string),
		pairChan:    make(chan string),
	}

	// Set up event handler
	wac.AddEventHandler(c.handleEvent)

	return c, nil
}

// Connect connects the client to WhatsApp
func (c *Client) Connect() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Update activity timestamp
	c.lastActivity = time.Now()

	// Check if already connected
	if c.client.IsConnected() {
		return nil
	}

	// Connect to WhatsApp
	err := c.client.Connect()
	if err != nil {
		c.status = StatusError
		c.connError = err.Error()
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Update status
	if c.client.IsLoggedIn() {
		c.status = StatusConnected
	} else {
		c.status = StatusDisconnected
	}

	return nil
}

// Disconnect disconnects the client from WhatsApp
func (c *Client) Disconnect() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Update activity timestamp
	c.lastActivity = time.Now()

	// Check if connected
	if !c.client.IsConnected() {
		return nil
	}

	// Disconnect
	c.client.Disconnect()
	c.status = StatusDisconnected

	return nil
}

// Logout logs out the client and removes device store
func (c *Client) Logout() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Update activity timestamp
	c.lastActivity = time.Now()

	// Check if logged in
	if !c.client.IsLoggedIn() {
		return nil
	}

	// Logout
	err := c.client.Logout()
	if err != nil {
		c.status = StatusError
		c.connError = err.Error()
		return fmt.Errorf("failed to logout: %w", err)
	}

	c.status = StatusLoggedOut
	return nil
}

// GenerateQR generates a QR code for authentication
func (c *Client) GenerateQR() (string, error) {
	c.mutex.Lock()

	// Update activity timestamp
	c.lastActivity = time.Now()

	// Check if already logged in
	if c.client.IsLoggedIn() {
		c.mutex.Unlock()
		return "", errors.New("already logged in")
	}
	
	// Disconnect first if already connected
	if c.client.IsConnected() {
		c.client.Disconnect()
	}

	// Get QR channel BEFORE connecting
	qrChan, err := c.client.GetQRChannel(context.Background())
	if err != nil {
		c.status = StatusError
		c.connError = err.Error()
		c.mutex.Unlock()
		return "", fmt.Errorf("failed to request QR: %w", err)
	}

	// Now connect after getting QR channel
	err = c.client.Connect()
	if err != nil {
		c.status = StatusError
		c.connError = err.Error()
		c.mutex.Unlock()
		return "", fmt.Errorf("failed to connect: %w", err)
	}

	// Release the mutex while waiting for the QR code
	c.mutex.Unlock()
	
	// Wait for QR code with timeout handling
	select {
	case evt := <-qrChan:
		// Check the event type
		if evt.Event == "code" {
			return evt.Code, nil
		} else if evt.Event == "err-client-outdated" {
			// This error means the WhatsApp Web version is outdated
			// Real solution would be to update whatsmeow library
			return "", errors.New("WhatsApp Web client outdated. Please update the whatsmeow library or try again later.")
		}
		return "", fmt.Errorf("unexpected QR event: %s", evt.Event)
		
	case <-time.After(30 * time.Second):
		return "", errors.New("timeout waiting for QR code")
	}
}

// PairPhone pairs the client with a phone number
func (c *Client) PairPhone(phoneNumber string) error {
	// Note: Direct phone pairing is not available in the current whatsmeow version
	// This is a placeholder that will always return an error
	return errors.New("phone pairing is not available in the current library version, please use QR code authentication")
}

// GetPairingCode gets the pairing code after a PairPhone request
func (c *Client) GetPairingCode() (string, error) {
	// Since phone pairing is not available, always return an error
	return "", errors.New("phone pairing is not available in the current library version")
}

// SendMessage sends a WhatsApp message
func (c *Client) SendMessage(recipient string, message string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Update activity timestamp
	c.lastActivity = time.Now()

	// Check if connected and logged in
	if !c.client.IsConnected() {
		return errors.New("not connected")
	}
	if !c.client.IsLoggedIn() {
		return errors.New("not logged in")
	}

	// Format the recipient properly for JID
	// Remove any potential + at the beginning
	if len(recipient) > 0 && recipient[0] == '+' {
		recipient = recipient[1:]
	}
	
	// Ensure the recipient has the right format: number@s.whatsapp.net
	if !strings.Contains(recipient, "@") {
		recipient = recipient + "@s.whatsapp.net"
	}

	// Parse recipient JID
	jid, err := types.ParseJID(recipient)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if jid.Server != types.DefaultUserServer {
		return fmt.Errorf("invalid recipient: not a user JID")
	}
	if jid.User == "" {
		return fmt.Errorf("invalid recipient: empty user")
	}

	// Create message
	msg := &waProto.Message{
		Conversation: proto.String(message),
	}

	// Send message
	_, err = c.client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

// GetState returns the current client state
func (c *Client) GetState() ClientState {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	connected := c.client.IsConnected()
	loggedIn := c.client.IsLoggedIn()
	
	var status ClientStatus
	if loggedIn {
		status = StatusConnected
	} else if connected {
		status = StatusDisconnected
	} else {
		status = c.status
	}
	
	// Get device info
	var pushName, phoneNumber string
	if c.deviceStore.PushName != "" {
		pushName = c.deviceStore.PushName
	}
	if loggedIn && c.client.Store.ID != nil {
		phoneNumber = c.client.Store.ID.User
	}

	return ClientState{
		ID:              c.ID,
		Status:          status,
		LastActivity:    c.lastActivity,
		Connected:       connected,
		LoggedIn:        loggedIn,
		PushName:        pushName,
		PhoneNumber:     phoneNumber,
		ConnectionError: c.connError,
	}
}

// Close closes the client and cleans up resources
func (c *Client) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Disconnect if connected
	if c.client.IsConnected() {
		c.client.Disconnect()
	}

	// No need to close the container in newer versions
	return nil
}

// SaveState saves the client state to a file
func (c *Client) SaveState() error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Get current state
	state := c.GetState()

	// Marshal to JSON
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to file
	stateFile := filepath.Join(c.dataDir, "state.json")
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// handleEvent handles WhatsApp events
func (c *Client) handleEvent(evt interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Update activity timestamp
	c.lastActivity = time.Now()

	// Handle specific events
	switch evt.(type) {
	case *events.QR:
		// For the QR event, we'll just send a notification
		// The actual QR code data is handled by the GetQRChannel method
		if c.qrChan != nil {
			select {
			case c.qrChan <- "qr_event_received":
				// Notification sent successfully
			default:
				// Channel not ready, ignore
			}
		}
	case *events.Connected:
		c.status = StatusConnected
		c.connError = ""
	case *events.Disconnected:
		if c.client.IsLoggedIn() {
			c.status = StatusDisconnected
		} else {
			c.status = StatusLoggedOut
		}
	}

	// Call the custom event handler if set
	if c.eventHandler != nil {
		c.eventHandler(evt)
	}
}

// SetEventHandler sets a custom event handler
func (c *Client) SetEventHandler(handler func(interface{})) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.eventHandler = handler
}
