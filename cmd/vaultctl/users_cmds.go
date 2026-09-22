package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
)

type userList struct {
	Users   []users.View `json:"users"`
	Folders []string     `json:"folders"`
}

// readPassword prompts without echo on a terminal; otherwise it reads one
// line from stdin so scripts can pipe a password in.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func newPassword(forWhom string) (string, error) {
	pw, err := readPassword(fmt.Sprintf("Password for %s (10+ characters): ", forWhom))
	if err != nil {
		return "", err
	}
	if err := auth.ValidatePassword(pw); err != nil {
		return "", fmt.Errorf("password: %w", err)
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		again, err := readPassword("Repeat password: ")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("the passwords don't match")
		}
	}
	return pw, nil
}

// parseFolders turns "Photos,Documents:ro,Shared" into grants.
func parseFolders(s string, role users.Role) ([]users.Folder, error) {
	var out []users.Folder
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, access := part, users.ReadWrite
		if i := strings.LastIndex(part, ":"); i > 0 {
			name = part[:i]
			switch part[i+1:] {
			case "ro":
				access = users.ReadOnly
			case "rw":
			default:
				return nil, fmt.Errorf("folder %q: use :rw or :ro", part)
			}
		}
		if role == users.Guest {
			access = users.ReadOnly
		}
		out = append(out, users.Folder{Name: name, Access: access})
	}
	return out, users.ValidateFolders(out)
}

func (a *app) usersCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		return a.usersList(ctx)
	}
	cmd, rest := args[0], args[1:]
	if len(rest) == 0 {
		return fmt.Errorf("usage: vaultctl users %s <username>", cmd)
	}
	name := strings.ToLower(rest[0])
	path := "/api/users/" + url.PathEscape(name)
	switch cmd {
	case "add":
		return a.usersAdd(ctx, name, rest[1:])
	case "disable", "enable":
		if err := a.call(ctx, http.MethodPut, path, map[string]bool{"disabled": cmd == "disable"}, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s is %sd.\n", name, cmd)
	case "reset-password":
		pw, err := newPassword(name)
		if err != nil {
			return err
		}
		if err := a.call(ctx, http.MethodPost, path+"/password", map[string]string{"password": pw}, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "Password for %s changed; they were signed out everywhere.\n", name)
	case "folders":
		if len(rest) < 2 {
			return errors.New(`usage: vaultctl users folders <username> "Photos,Documents:ro"`)
		}
		var list userList
		if err := a.call(ctx, http.MethodGet, "/api/users", nil, &list); err != nil {
			return err
		}
		role := users.Family
		for _, u := range list.Users {
			if u.Username == name {
				role = u.Role
			}
		}
		grants, err := parseFolders(rest[1], role)
		if err != nil {
			return err
		}
		if err := a.call(ctx, http.MethodPut, path, map[string]any{"folders": grants}, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "Folders for %s updated.\n", name)
	case "remove":
		yes := len(rest) > 1 && (rest[1] == "--yes" || rest[1] == "-y")
		if !yes && !confirm(fmt.Sprintf("Remove %s? Their files stay in the Vault.", name)) {
			return errors.New("cancelled")
		}
		if err := a.call(ctx, http.MethodDelete, path, nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s removed. No files were deleted.\n", name)
	default:
		return fmt.Errorf("unknown users command %q", cmd)
	}
	return nil
}

func (a *app) usersList(ctx context.Context) error {
	var list userList
	if err := a.call(ctx, http.MethodGet, "/api/users", nil, &list); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(list)
	}
	fmt.Fprintln(a.out, "USERS")
	fmt.Fprintln(a.out)
	if len(list.Users) == 0 {
		fmt.Fprintln(a.out, "  No accounts yet. Create yours with: vaultctl users add <name>")
		return nil
	}
	for _, u := range list.Users {
		var flags []string
		if u.Disabled {
			flags = append(flags, "DISABLED")
		}
		if u.TOTPEnabled {
			flags = append(flags, "2FA")
		}
		folders := "all folders"
		if !u.AllFolders {
			var fs []string
			for _, f := range u.Folders {
				s := f.Name
				if f.Access == users.ReadOnly {
					s += " (read only)"
				}
				fs = append(fs, s)
			}
			folders = strings.Join(fs, ", ")
			if folders == "" {
				folders = "no folders"
			}
		}
		fmt.Fprintf(a.out, "  %-16s %-7s %-12s %s\n", u.Username, u.Role, strings.Join(flags, " "), folders)
	}
	return nil
}

func (a *app) usersAdd(ctx context.Context, name string, args []string) error {
	role := users.Family
	folderSpec := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				return errors.New("--role needs admin, family or guest")
			}
			role = users.Role(args[i+1])
			i++
		case "--folders":
			if i+1 >= len(args) {
				return errors.New(`--folders needs a list like "Photos,Documents:ro"`)
			}
			folderSpec = args[i+1]
			i++
		default:
			return fmt.Errorf("unexpected argument %q", args[i])
		}
	}
	if !role.Valid() {
		return errors.New("--role must be admin, family or guest")
	}
	if err := users.ValidateUsername(name); err != nil {
		return err
	}
	body := map[string]any{"username": name, "role": role}
	if folderSpec != "" {
		grants, err := parseFolders(folderSpec, role)
		if err != nil {
			return err
		}
		body["folders"] = grants
	}
	pw, err := newPassword(name)
	if err != nil {
		return err
	}
	body["password"] = pw
	var resp struct {
		User users.View `json:"user"`
	}
	if err := a.call(ctx, http.MethodPost, "/api/users", body, &resp); err != nil {
		return err
	}
	folders := "all folders"
	if !resp.User.AllFolders {
		var fs []string
		for _, f := range resp.User.Folders {
			fs = append(fs, f.Name+map[users.Access]string{users.ReadOnly: " (read only)"}[f.Access])
		}
		folders = strings.Join(fs, ", ")
	}
	fmt.Fprintf(a.out, "Created %s (%s): %s\n", resp.User.Username, resp.User.Role, folders)
	fmt.Fprintln(a.out, "They sign in at the Vault address with this username and password.")
	return nil
}

func (a *app) filesCmd(ctx context.Context) error {
	var st struct {
		State   string `json:"state"`
		Message string `json:"message"`
		URL     string `json:"url"`
	}
	if err := a.call(ctx, http.MethodGet, "/api/files", nil, &st); err != nil {
		return err
	}
	if a.json {
		return a.emitJSON(st)
	}
	switch st.State {
	case "running":
		fmt.Fprintf(a.out, "Files is running: %s%s\n", a.baseURL(), st.URL)
	case "not_installed":
		fmt.Fprintln(a.out, "Files is not installed. Run: ./scripts/build-sftpgo.sh  (or re-run ./scripts/install.sh)")
	default:
		fmt.Fprintf(a.out, "Files: %s. %s\n", strings.ReplaceAll(st.State, "_", " "), st.Message)
	}
	return nil
}
