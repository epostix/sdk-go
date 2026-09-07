package epostix

import (
	"encoding/json"
	"fmt"
)

type Mailbox struct {
	Email string
	Name  string
}

func Addr(email string) Mailbox {
	return Mailbox{Email: email}
}

func Named(name, email string) Mailbox {
	return Mailbox{Email: email, Name: name}
}

func (m Mailbox) String() string {
	if m.Name == "" {
		return m.Email
	}

	return fmt.Sprintf("%s <%s>", m.Name, m.Email)
}

func (m Mailbox) MarshalJSON() ([]byte, error) {
	if m.Name == "" {
		return json.Marshal(m.Email)
	}

	return json.Marshal(struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}{Email: m.Email, Name: m.Name})
}

func (m *Mailbox) UnmarshalJSON(data []byte) error {
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		m.Email = plain
		m.Name = ""

		return nil
	}

	var object struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}

	if err := json.Unmarshal(data, &object); err != nil {
		return fmt.Errorf("decode Mailbox: %w", err)
	}

	m.Email = object.Email
	m.Name = object.Name

	return nil
}
