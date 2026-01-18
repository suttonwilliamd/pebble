package sync

import (
	"os"

	"golang.org/x/crypto/ssh"
)

// SSHAuthentication is responsible for handling SSH authentication
type SSHAuthentication struct {
	Config *ssh.ClientConfig
}

// NewSSHAuthentication creates a new SSHAuthentication instance
func NewSSHAuthentication(username, privateKeyPath string) (*SSHAuthentication, error) {
	// Read the private key
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, err
	}

	// Parse the private key
	privateKey, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
	}

	// Create the SSH client config
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(privateKey),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return &SSHAuthentication{Config: config}, nil
}

// Connect connects to the SSH server
func (sa *SSHAuthentication) Connect(addr string) (*ssh.Client, error) {
	// Connect to the SSH server
	conn, err := ssh.Dial("tcp", addr, sa.Config)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// ExecuteCommand executes a command on the SSH server
func (sa *SSHAuthentication) ExecuteCommand(conn *ssh.Client, command string) (string, error) {
	// Create a session
	session, err := conn.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	// Execute the command
	output, err := session.CombinedOutput(command)
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// Close closes the SSH connection
func (sa *SSHAuthentication) Close(conn *ssh.Client) error {
	return conn.Close()
}
