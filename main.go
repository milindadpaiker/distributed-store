package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
)

type KVStore struct {
	mu   sync.Mutex
	data map[string]string
}

func NewKVStore() *KVStore {
	return &KVStore{
		data: make(map[string]string),
	}
}

func (s *KVStore) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

func (s *KVStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	val, ok := s.data[key]
	return val, ok
}

// Command represents a structured command for the state machine
type Command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// FSM implements raft.FSM for state machine replication
type FSM struct {
	store *KVStore
}

func NewFSM(store *KVStore) *FSM {
	return &FSM{store: store}
}

func (f *FSM) Apply(log *raft.Log) interface{} {
	// Try to parse as JSON Command first
	var cmd Command
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		// Fallback to original string format for backward compatibility
		command := string(log.Data)
		parts := strings.SplitN(command, ":", 2)
		if len(parts) != 2 {
			return nil
		}
		key, value := parts[0], parts[1]
		f.store.Set(key, value)
		return nil
	}

	switch cmd.Op {
	case "set":
		f.store.Set(cmd.Key, cmd.Value)
	}
	return nil
}

// Snapshot returns a snapshot of the key-value store
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	// Clone the map to avoid concurrent access issues
	clone := make(map[string]string)
	for k, v := range f.store.data {
		clone[k] = v
	}

	return &fsmSnapshot{store: clone}, nil
}

// Restore restores the key-value store to a previous state
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return err
	}

	// Reset the store
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	f.store.data = store

	return nil
}

// fsmSnapshot implements the raft.FSMSnapshot interface
type fsmSnapshot struct {
	store map[string]string
}

// Persist saves the snapshot to the given sink
func (f *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	data, err := json.Marshal(f.store)
	if err != nil {
		sink.Cancel()
		return err
	}

	if _, err := sink.Write(data); err != nil {
		sink.Cancel()
		return err
	}

	return sink.Close()
}

// Release is a no-op
func (f *fsmSnapshot) Release() {}

func handleDataRequest(w http.ResponseWriter, req *http.Request, store *KVStore) {
	// Make a copy of the data to avoid concurrent access issues
	store.mu.Lock()
	data := make(map[string]string)
	for k, v := range store.data {
		data[k] = v
	}
	store.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	jsonData, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "Error encoding JSON", http.StatusInternalServerError)
		log.Printf("Error marshaling JSON: %s", err)
		return
	}

	if _, err := w.Write(jsonData); err != nil {
		log.Printf("Error writing response: %s", err)
	}
}

func main() {
	// Get node configuration from environment
	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatalf("Failed to get hostname: %v", err)
		}
		nodeID = hostname
	}

	// Configure logger with node ID prefix
	log.SetPrefix(fmt.Sprintf("[%s] ", nodeID))
	log.Printf("Starting node with ID: %s", nodeID)

	// Get network configuration
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "0.0.0.0:8080"
	}

	advertiseAddr := os.Getenv("ADVERTISE_ADDR")
	if advertiseAddr == "" {
		advertiseAddr = bindAddr
	}

	log.Printf("Binding to %s, advertising as %s", bindAddr, advertiseAddr)

	// Get data directory from environment or use default
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		// Create a node-specific directory under /app/data
		dataDir = filepath.Join("/app/data", nodeID)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	log.Printf("Using data directory: %s", dataDir)

	// Initialize KV store and FSM
	store := NewKVStore()
	fsm := NewFSM(store)

	// Configure Raft
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)

	// Create Raft storage
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft-log.db"))
	if err != nil {
		log.Fatalf("Failed to create log store: %v", err)
	}

	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft-stable.db"))
	if err != nil {
		log.Fatalf("Failed to create stable store: %v", err)
	}

	snapshotStore, err := raft.NewFileSnapshotStore(dataDir, 3, os.Stderr)
	if err != nil {
		log.Fatalf("Failed to create snapshot store: %v", err)
	}


	// Resolve the advertise address
	advertiseAddrTCP, err := net.ResolveTCPAddr("tcp", advertiseAddr)
	if err != nil {
		log.Fatalf("Failed to resolve advertise address: %v", err)
	}

	transport, err := raft.NewTCPTransport(bindAddr, advertiseAddrTCP, 3, 10*time.Second, os.Stderr)
	if err != nil {
		log.Fatalf("Failed to create transport: %v", err)
	}

	// Create Raft instance
	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		log.Fatalf("Failed to create Raft instance: %v", err)
	}

	// Check if this node should bootstrap the cluster
	bootstrap := os.Getenv("BOOTSTRAP") == "true"
	joinAddr := os.Getenv("JOIN_ADDR")

	if bootstrap {
		log.Println("Bootstrapping new Raft cluster")
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      config.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		future := r.BootstrapCluster(configuration)
		if err := future.Error(); err != nil && err != raft.ErrCantBootstrap {
			log.Fatalf("Failed to bootstrap cluster: %v", err)
		}
	} else if joinAddr != "" {
		// Set up a goroutine to attempt joining after startup
		// This helps with development mode where nodes might restart frequently
		go func() {
			// Wait a bit for the cluster to stabilize
			time.Sleep(2 * time.Second)
			
			log.Printf("Attempting to join cluster at %s", joinAddr)
			
			// Create join request
			joinURL := fmt.Sprintf("http://%s/join", joinAddr)
			joinData := fmt.Sprintf(`{"node_id":"%s","address":"%s"}`, nodeID, advertiseAddr)
			
			// Retry joining with exponential backoff
			maxRetries := 10
			for i := 0; i < maxRetries; i++ {
				resp, err := http.Post(joinURL, "application/json", strings.NewReader(joinData))
				
				if err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						log.Println("Successfully joined the cluster")
						break
					} else {
						body, _ := io.ReadAll(resp.Body)
						log.Printf("Join failed with status %d: %s", resp.StatusCode, string(body))
					}
				} else {
					log.Printf("Failed to join cluster: %v", err)
				}
				
				// Backoff before retry
				retryDelay := time.Duration(1<<uint(i)) * time.Second
				if retryDelay > 30*time.Second {
					retryDelay = 30 * time.Second
				}
				
				log.Printf("Retrying join in %v...", retryDelay)
				time.Sleep(retryDelay)
			}
		}()
	} else {
		log.Println("Neither bootstrapping nor joining, running in standalone mode")
	}

	// Set up HTTP handlers
	http.HandleFunc("/join", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		
		var joinReq struct {
			NodeID  string `json:"node_id"`
			Address string `json:"address"`
		}
		
		if err := json.NewDecoder(req.Body).Decode(&joinReq); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}
		
		// Check if we're the leader
		if r.State() != raft.Leader {
			leader := string(r.Leader())
			if leader == "" {
				http.Error(w, "No leader available", http.StatusServiceUnavailable)
				return
			}
			
			// Redirect to leader
			url := fmt.Sprintf("http://%s/join", leader)
			http.Redirect(w, req, url, http.StatusTemporaryRedirect)
			return
		}
		
		log.Printf("Received join request from %s at %s", joinReq.NodeID, joinReq.Address)
		
		// Add the node to the cluster
		future := r.AddVoter(
			raft.ServerID(joinReq.NodeID),
			raft.ServerAddress(joinReq.Address),
			0,
			0,
		)
		
		if err := future.Error(); err != nil {
			log.Printf("Error adding voter: %v", err)
			http.Error(w, fmt.Sprintf("Failed to add node: %v", err), http.StatusInternalServerError)
			return
		}
		
		log.Printf("Node %s successfully joined the cluster", joinReq.NodeID)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Successfully joined the cluster")
	})

	http.HandleFunc("/set", func(w http.ResponseWriter, req *http.Request) {
		// Check if we're the leader
		if r.State() != raft.Leader {
			leader := string(r.Leader())
			if leader == "" {
				http.Error(w, "No leader available", http.StatusServiceUnavailable)
				return
			}
			
			// Redirect to leader
			url := fmt.Sprintf("http://%s/set?%s", leader, req.URL.RawQuery)
			http.Redirect(w, req, url, http.StatusTemporaryRedirect)
			return
		}
		
		key := req.URL.Query().Get("key")
		value := req.URL.Query().Get("value")
		
		if key == "" {
			http.Error(w, "Key is required", http.StatusBadRequest)
			return
		}
		
		// Create a structured command
		cmd := Command{
			Op:    "set",
			Key:   key,
			Value: value,
		}
		
		cmdData, err := json.Marshal(cmd)
		if err != nil {
			http.Error(w, "Error encoding command", http.StatusInternalServerError)
			return
		}
		
		// Apply to Raft
		future := r.Apply(cmdData, 500*time.Millisecond)
		if err := future.Error(); err != nil {
			log.Printf("Error applying command: %v", err)
			http.Error(w, "Error applying command", http.StatusInternalServerError)
			return
		}
		
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/get", func(w http.ResponseWriter, req *http.Request) {
		key := req.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "Key is required", http.StatusBadRequest)
			return
		}
		
		value, ok := store.Get(key)
		if !ok {
			http.Error(w, "Key not found", http.StatusNotFound)
			return
		}
		
		w.Write([]byte(value))
	})

	http.HandleFunc("/data", func(w http.ResponseWriter, req *http.Request) {
		handleDataRequest(w, req, store)
	})

	http.HandleFunc("/leader", func(w http.ResponseWriter, req *http.Request) {
		leader := string(r.Leader())
		isLeader := r.State() == raft.Leader
		
		response := struct {
			Leader   string `json:"leader"`
			IsLeader bool   `json:"is_leader"`
			NodeID   string `json:"node_id"`
			State    string `json:"state"`
		}{
			Leader:   leader,
			IsLeader: isLeader,
			NodeID:   nodeID,
			State:    r.State().String(),
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	httpBindAddr := os.Getenv("HTTP_BIND_ADDR")
	if httpBindAddr == "" {
		httpBindAddr = ":8081" // Default to 8081 for HTTP to avoid conflict
	}	

	log.Printf("HTTP server listening on %s", httpBindAddr)
	log.Fatal(http.ListenAndServe(httpBindAddr, nil))
}
