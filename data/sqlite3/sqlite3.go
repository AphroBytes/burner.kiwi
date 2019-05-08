package sqlite3

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/haydenwoodhead/burner.kiwi/data"
	"github.com/haydenwoodhead/burner.kiwi/metrics"
	"github.com/jmoiron/sqlx"

	_ "github.com/mattn/go-sqlite3" // import go-sqlite3 here rather than main
)

// SQLite3 implements the database interface for sqlite3
type SQLite3 struct {
	*sqlx.DB
}

// GetSQLite3DB returns a new sqlite db or panics
func GetSQLite3DB(dbURL string) *SQLite3 {
	s := &SQLite3{sqlx.MustOpen("sqlite3", dbURL)}
	go func() {
		s.CreateTables()

		t := time.Now().Unix()
		var active int
		err := s.Get(&active, "select count(*) from inbox WHERE ttl > $1", t)
		if err != nil {
			log.Println("Failed to get number of active inboxes")
		}

		metrics.ActiveInboxes.Set(float64(active))

		for {
			count, err := s.runTTLDelete()
			if err != nil {
				log.Printf("Failed to delete old rows from db: %v\n", err)
				break
			}
			log.Printf("Deleted %v old inboxes from db\n", count)
			metrics.ActiveInboxes.Sub(float64(count))
			time.Sleep(30 * time.Minute)
		}
	}()
	return s
}

// CreateTables creates the databse tables or panics
func (s *SQLite3) CreateTables() {
	s.MustExec(`create table if not exists inbox (
		id uuid not null unique,
		address text not null unique,
		created_at numeric,
		created_by text,
		mg_routeid text,
		ttl numeric,
		failed_to_create bool,
		primary key (id)
	);
	
	create table if not exists message (
		inbox_id uuid references inbox(id) on delete cascade,
		message_id uuid not null unique,
		received_at numeric,
		mg_id text,
		sender text,
		from_address text,
		subject text,
		body_html text,
		body_plain text,
		ttl numeric,
		primary key (message_id)
	);`)
}

// SaveNewInbox saves a new inbox
func (s *SQLite3) SaveNewInbox(i data.Inbox) error {
	_, err := s.NamedExec(
		"INSERT INTO inbox (id, address, created_at, created_by, mg_routeid, ttl, failed_to_create) VALUES (:id, :address, :created_at, :created_by, :mg_routeid, :ttl, :failed_to_create)",
		map[string]interface{}{
			"id":               i.ID,
			"address":          i.Address,
			"created_at":       i.CreatedAt,
			"created_by":       i.CreatedBy,
			"mg_routeid":       i.MGRouteID,
			"ttl":              i.TTL,
			"failed_to_create": i.FailedToCreate,
		},
	)
	if err == nil {
		metrics.ActiveInboxes.Inc()
	}
	return err
}

// GetInboxByID gets an inbox by id
func (s *SQLite3) GetInboxByID(id string) (data.Inbox, error) {
	var i data.Inbox
	err := s.Get(&i, "SELECT id, address, created_at, created_by, mg_routeid, ttl, failed_to_create FROM inbox WHERE id = $1", id)
	return i, err
}

// EmailAddressExists checks if an address already exists
func (s *SQLite3) EmailAddressExists(email string) (bool, error) {
	var count int
	err := s.Get(&count, "SELECT COUNT(*) FROM inbox WHERE address = $1", email)
	return count == 1, err
}

// SetInboxCreated creates a new inbox
func (s *SQLite3) SetInboxCreated(i data.Inbox) error {
	_, err := s.Exec("UPDATE inbox SET failed_to_create = 'false', mg_routeid = $1 WHERE id = $2", i.MGRouteID, i.ID)
	return err
}

// SaveNewMessage saves a new message to the db
func (s *SQLite3) SaveNewMessage(m data.Message) error {
	_, err := s.NamedExec("INSERT INTO message (inbox_id, message_id, received_at, mg_id, sender, from_address, subject, body_html, body_plain, ttl) VALUES (:inbox_id, :message_id, :received_at, :mg_id, :sender, :from_address, :subject, :body_html, :body_plain, :ttl)",
		map[string]interface{}{
			"inbox_id":     m.InboxID,
			"message_id":   m.ID,
			"received_at":  m.ReceivedAt,
			"mg_id":        m.MGID,
			"sender":       m.Sender,
			"from_address": m.From,
			"subject":      m.Subject,
			"body_html":    m.BodyHTML,
			"body_plain":   m.BodyPlain,
			"ttl":          m.TTL,
		},
	)
	return err
}

// GetMessagesByInboxID gets all messages for an inbox
func (s *SQLite3) GetMessagesByInboxID(id string) ([]data.Message, error) {
	var msgs []data.Message
	err := s.Select(&msgs, "SELECT inbox_id, message_id, received_at, mg_id, sender, from_address, subject, body_html, body_plain, ttl FROM message WHERE inbox_id = $1", id)
	return msgs, err
}

// GetMessageByID gets a single message
func (s *SQLite3) GetMessageByID(i, m string) (data.Message, error) {
	var msg data.Message
	err := s.Get(&msg, "SELECT inbox_id, message_id, received_at, mg_id, sender, from_address, subject, body_html, body_plain, ttl FROM message WHERE inbox_id = $1 and message_id = $2", i, m)
	if err == sql.ErrNoRows {
		return msg, data.ErrMessageDoesntExist
	}

	return msg, err
}

func (s *SQLite3) runTTLDelete() (int, error) {
	t := time.Now().Unix()
	res, err := s.Exec("DELETE from inbox WHERE ttl < $1", t)
	if err != nil {
		return -1, fmt.Errorf("SQLite3.runTTLDelete failed with err=%v", err)
	}
	count, err := res.RowsAffected()
	return int(count), err
}
