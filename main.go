package main

import (
 "encoding/json"
 "log"
 "net/http"
 "strings"
 "sync"
 "time"

 "github.com/gorilla/websocket"
)

type Room struct {
 clients    map[*websocket.Conn]bool
 broadcast  chan *BroadcastMessage
 register   chan *websocket.Conn
 unregister chan *websocket.Conn
 mutex      sync.Mutex
 id         string                // Room ID for cleanup
 cleanup    chan<- string         // Channel to signal room deletion
}

func newRoom(id string, cleanup chan<- string) *Room {
 return &Room{
  clients:    make(map[*websocket.Conn]bool),
  broadcast:  make(chan *BroadcastMessage),
  register:   make(chan *websocket.Conn),
  unregister: make(chan *websocket.Conn),
  id:         id,
  cleanup:    cleanup,
 }
}

func (r *Room) run() {
 for {
  select {
  case client := <-r.register:
   r.mutex.Lock()
   r.clients[client] = true
   // Send welcome message to confirm connection
  // welcomeMsg := map[string]string{"type": "connect", "message": "Connected successfully"}
  // welcomeData, _ := json.Marshal(welcomeMsg)
  // client.WriteMessage(websocket.TextMessage, welcomeData)
   r.mutex.Unlock()
  case client := <-r.unregister:
   r.mutex.Lock()
   if _, ok := r.clients[client]; ok {
    delete(r.clients, client)
    client.Close()

    // Notify other clients about the disconnection
    disconnectMsg := map[string]string{"type": "disconnect", "message": "A user has disconnected"}
    disconnectData, _ := json.Marshal(disconnectMsg)

    for c := range r.clients {
     err := c.WriteMessage(websocket.TextMessage, disconnectData)
     if err != nil {
      log.Printf("Error sending disconnect notification: %v", err)
      c.Close()
      delete(r.clients, c)
     }
    }

    // If no clients remain, signal for room deletion
    if len(r.clients) == 0 {
     log.Printf("No clients remaining in room %s, marking for deletion", r.id)
     r.mutex.Unlock()
     r.cleanup <- r.id
     return
    }
   }
   r.mutex.Unlock()
  case message := <-r.broadcast:
   r.mutex.Lock()
   for client := range r.clients {
    if client != message.sender {
     err := client.WriteMessage(websocket.TextMessage, message.data)
     if err != nil {
      log.Printf("Error broadcasting message: %v", err)
      client.Close()
      delete(r.clients, client)
     }
    }
   }

   // If all clients disconnected while processing, clean up room
   if len(r.clients) == 0 {
    log.Printf("No clients remaining in room %s after broadcast, marking for deletion", r.id)
    r.mutex.Unlock()
    r.cleanup <- r.id
    return
   }
   r.mutex.Unlock()
  }
 }
}

var upgrader = websocket.Upgrader{
 ReadBufferSize:  1024,
 WriteBufferSize: 1024,
 CheckOrigin: func(r *http.Request) bool {
  return true // Allow all connections
 },
}

type Message struct {
 data   []byte
 sender *websocket.Conn
}

type BroadcastMessage struct {
 data   []byte
 sender *websocket.Conn
}

func main() {
 rooms := make(map[string]*Room)
 var roomsMutex sync.Mutex
 roomCleanup := make(chan string)

 // Process room cleanup requests
 go func() {
  for roomID := range roomCleanup {
   roomsMutex.Lock()
   delete(rooms, roomID)
   log.Printf("Room %s has been deleted", roomID)
   roomsMutex.Unlock()
  }
 }()

 // Add a simple status endpoint
 http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  w.Write([]byte("Server is running"))
 })

 http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
  // Extract room ID from path
  path := strings.TrimPrefix(r.URL.Path, "/")
  roomID := path

  if roomID == "" {
   http.Error(w, "Room ID is required", http.StatusBadRequest)
   return
  }

  // Create room if it doesn't exist
  roomsMutex.Lock()
  if _, exists := rooms[roomID]; !exists {
   rooms[roomID] = newRoom(roomID, roomCleanup)
   go rooms[roomID].run()
   log.Printf("Created new room: %s", roomID)
  }
  room := rooms[roomID]
  roomsMutex.Unlock()

  // Set headers for WebSocket
  w.Header().Set("Access-Control-Allow-Origin", "*")
  w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
  w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

  // Upgrade connection to websocket
  conn, err := upgrader.Upgrade(w, r, nil)
  if err != nil {
   log.Printf("Failed to upgrade connection: %v", err)
   return
  }

  // Set ping handler to keep connection alive
  conn.SetPingHandler(func(string) error {
   conn.WriteControl(websocket.PongMessage, []byte{}, time.Now().Add(time.Second))
   return nil
  })

  // Configure connection to avoid timeouts
  conn.SetReadDeadline(time.Time{}) // No deadline
  conn.SetWriteDeadline(time.Time{}) // No deadline

  log.Printf("Client connected to room: %s", roomID)
  room.register <- conn

  defer func() {
   log.Printf("Client disconnected from room: %s", roomID)
   room.unregister <- conn
  }()

  for {
   messageType, message, err := conn.ReadMessage()
   if err != nil {
    log.Printf("Error reading message: %v", err)
    break
   }

   log.Printf("Received message: %s", string(message))

   // Echo the message back to confirm receipt
   if err := conn.WriteMessage(messageType, message); err != nil {
    log.Printf("Error echoing message: %v", err)
   }

   // Broadcast message to all in the room
   room.broadcast <- &BroadcastMessage{
    data:   message,
    sender: conn,
   }
  }
 })

 log.Println("Server starting on :8080")
 log.Fatal(http.ListenAndServe("0.0.0.0:8080", nil))
}
