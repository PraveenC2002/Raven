package raven

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

type ravenCLI struct {
	cli *cobra.Command
	db  *sql.DB
}

func NewRavenCLI() (RavenCLI, error) {

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dbPath := filepath.Join(homeDir, ".raven", "raven.db")
	db, err := openDBPath(dbPath)
	if err != nil {
		return nil, err
	}
	// .db done

	reg := &registry{
		db: db,
	}
	return &ravenCLI{
		cli: setupCmd(reg),
		db:  db,
	}, nil
}

func (r *ravenCLI) Run() error {
	defer r.db.Close()
	
	return nil
}

func setupCmd(r Registry) *cobra.Command {

	rootCmd := &cobra.Command{
		Use:   "raven",
		Short: "raven CLI",
	}

	vmCmd := &cobra.Command{
		Use:   "vm",
		Short: "virtual machines for raven",
	}

	addVmCmd := newAddVmCmd(r)
	removeVmCmd := newRemoveVmCmd(r)
	updateVmCmd := newUpdateVmCmd(r)
	listVmCmd := newListVmCmd(r)
	showVmCmd := newShowVmCmd(r)

	vmCmd.AddCommand(addVmCmd, removeVmCmd, updateVmCmd, listVmCmd, showVmCmd)

	initCmd := newInitCmd(r)

	rootCmd.AddCommand(vmCmd, initCmd)

	return rootCmd
}

func huhMachineForm(m *machine, action string) (bool, error) {

	nameInp := huh.NewInput().
		Title("Name").
		Description("must be unique").
		Value(&m.Name).
		Validate(func(s string) error {
			if s == "" {
				return errors.New("name is required")
			}
			return nil
		})

	descriptionInp := huh.NewText().
		Title("Description").
		Placeholder("describe your machine").
		Value(&m.Description).
		Validate(func(s string) error {
			if s == "" {
				return errors.New("description is required")
			}
			return nil
		})

	hostInp := huh.NewInput().
		Title("Host").
		Description("hostname or IP address of the machine").
		Value(&m.Host).
		Validate(func(s string) error {
			if s == "" {
				return errors.New("host is required")
			}
			return nil
		})

	port := "22"
	if m.Port != 0 {
		port = strconv.Itoa(m.Port)
	}
	portInp := huh.NewInput().
		Title("Port").
		Description("SSH Port").
		Value(&port).
		Validate(func(s string) error {
			p, err := strconv.Atoi(s)
			if err != nil {
				return errors.New("must be a number")
			}
			if p < 1 || p > 65535 {
				return errors.New("port out of range")
			}
			return nil
		})

	sshUserInp := huh.NewInput().
		Title("SSH User").
		Description("SSH login user").
		Value(&m.SshUser).
		Validate(func(s string) error {
			if s == "" {
				return errors.New("SSH User is required")
			}
			return nil
		})

	keyPathInp := huh.NewInput().
		Title("Key Path").
		Description("path to private SSH key").
		Value(&m.KeyPath).
		Validate(func(s string) error {
			if s == "" {
				return errors.New("key path is required")
			}
			return nil
		})

	hostKeyInp := huh.NewInput().
		Title("Host Key").
		Description("public host key to verify machine identity").
		Value(&m.HostKey).
		Validate(func(s string) error {
			if s == "" {
				return errors.New("host key is required")
			}
			return nil
		})

	var confirmed bool
	confirmInp := huh.NewConfirm().
		Title(fmt.Sprintf("%s this machine?", action)).
		Affirmative(action).
		Negative("Cancel").
		Value(&confirmed)

	identityGrp := huh.NewGroup(
		nameInp,
		descriptionInp,
	).
		Title("Identity").
		Description("how raven recognizes this machine")

	connectionGrp := huh.NewGroup(
		hostInp,
		portInp,
		sshUserInp,
		keyPathInp,
		hostKeyInp,
		confirmInp,
	).
		Title("Connection").
		Description("SSH details")

	if err := huh.NewForm(identityGrp, connectionGrp).
		WithTheme(huh.ThemeCharm()).
		Run(); err != nil {
		return false, err
	}

	if !confirmed {
		return false, nil
	}

	p, _ := strconv.Atoi(port)
	m.Port = p

	return true, nil
}

// TODO: Enforce hard 64 chars limit on machine name
func newAddVmCmd(r Registry) *cobra.Command {

	add := func(cmd *cobra.Command, args []string) error {

		m := &machine{}
		ok, err := huhMachineForm(m, "Add")
		if err != nil {
			return err
		}

		if !ok {
			return nil
		}

		return r.addVm(m)
	}

	addVmCmd := &cobra.Command{
		Use:   "add",
		Short: "add a virtual machine to raven",
		RunE:  add,
	}

	return addVmCmd
}

func newRemoveVmCmd(r Registry) *cobra.Command {

	remove := func(cmd *cobra.Command, args []string) error {
		if err := r.removeVm(args[0]); err != nil {
			return err
		}
		return nil
	}

	removeVmCmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "remove a virtual machine from raven",
		Args:  cobra.ExactArgs(1),
		RunE:  remove,
	}

	return removeVmCmd
}

func newUpdateVmCmd(r Registry) *cobra.Command {

	update := func(cmd *cobra.Command, args []string) error {

		m, err := r.getVm(args[0])
		if err != nil {
			return err
		}

		ok, err := huhMachineForm(m, "Update")
		if err != nil {
			return err
		}

		if !ok {
			return nil
		}

		return r.updateVm(m)
	}

	var updateVmCmd = &cobra.Command{
		Use:   "update <name>",
		Short: "update virtual machine config on raven",
		Args:  cobra.ExactArgs(1),
		RunE:  update,
	}

	return updateVmCmd
}

func listMachines(machines []*machine) {

	var (
		white       = lipgloss.Color("#FAFAFA")
		headerStyle = lipgloss.NewStyle().
				Foreground(white).
				Bold(true).
				Align(lipgloss.Center)
		cellStyle     = lipgloss.NewStyle().Padding(0, 1).Foreground(white)
		labelColStyle = cellStyle.Bold(true)
	)

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(white)).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == table.HeaderRow:
				return headerStyle
			case col == 0:
				return labelColStyle
			default:
				return cellStyle
			}
		}).
		Headers("NAME", "HOST", "PORT", "SSH-USER")

	for _, m := range machines {
		t.Row(m.Name, m.Host, strconv.Itoa(m.Port), m.SshUser)
	}

	lipgloss.Println(t)
}

func newListVmCmd(r Registry) *cobra.Command {

	list := func(cmd *cobra.Command, args []string) error {
		machines, err := r.listVm()
		if err != nil {
			return err
		}

		listMachines(machines)

		return nil
	}

	listVmCmd := &cobra.Command{
		Use:   "list",
		Short: "list all virtual machines on raven",
		RunE:  list,
	}

	return listVmCmd
}

func showMachine(m *machine) {
	var (
		labelStyle = lipgloss.NewStyle().
				Bold(true).
				Width(14)

		valueStyle = lipgloss.NewStyle()

		boxStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				Padding(1, 2)
	)

	row := func(label, value string) string {
		return lipgloss.JoinHorizontal(
			lipgloss.Top,
			labelStyle.Render(label),
			valueStyle.Render(value),
		)
	}

	descStyle := lipgloss.NewStyle().
		Width(60)

	kv := lipgloss.JoinVertical(
		lipgloss.Left,
		row("ID :", m.Id.String()),
		row("Name :", m.Name),
		row("Host :", m.Host),
		row("Port :", strconv.Itoa(m.Port)),
		row("SSH User :", m.SshUser),
		row("Key Path :", m.KeyPath),
		row("Host Key :", m.HostKey),
		row("Created :", m.CreatedAt.Format(time.RFC3339)),
	)

	descBlock := lipgloss.JoinVertical(
		lipgloss.Left,
		labelStyle.Render("Description :"),
		descStyle.Render(m.Description),
	)

	content := lipgloss.JoinVertical(lipgloss.Left, kv, "", descBlock)

	lipgloss.Println(boxStyle.Render(content))
}

func newShowVmCmd(r Registry) *cobra.Command {

	show := func(cmd *cobra.Command, args []string) error {

		m, err := r.getVm(args[0])
		if err != nil {
			return err
		}

		showMachine(m)

		return nil
	}

	showVmCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "show a virtual machine's config on raven",
		Args:  cobra.ExactArgs(1),
		RunE:  show,
	}

	return showVmCmd
}

func newInitCmd(r Registry) *cobra.Command {

	var (
		ownerId int64
	)

	runInit := func(cmd *cobra.Command, args []string) error {
		o := &owner{
			OwnerId: tgInt(ownerId),
		}

		return r.initUser(o)
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "initialize raven with your telegram user id",
		Long:  "Initialize Raven with your telegram user id.\n" + "You can get it by pinging @userinfobot with /start on Telegram",
		RunE:  runInit,
	}

	initCmd.Flags().Int64Var(&ownerId, "tg-id", 0, "telegram id, get it by pinging @userinfobot at telegram")
	initCmd.MarkFlagRequired("tg-id")

	return initCmd
}
