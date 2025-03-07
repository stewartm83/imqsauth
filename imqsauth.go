package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/IMQS/authaus"
	"github.com/IMQS/cli"
	"github.com/IMQS/gowinsvc/service"
	auth "github.com/IMQS/imqsauth/auth"
	serviceconfig "github.com/IMQS/serviceconfigsgo"
)

func isRunningOnLinuxOutsideOfDocker() bool {
	return !serviceconfig.IsContainer() && runtime.GOOS != "windows"
}

func main() {
	app := cli.App{}

	app.Description = "imqsauth -c=configfile [options] command"
	app.DefaultExec = exec

	app.AddCommand("createdb", "Create the postgres database")
	app.AddCommand("resetauthgroups", "Reset the [admin,enabled] groups, and few others")
	app.AddCommand("rollbackgroups", "Roll back undesired auto-created groups from September 2019")

	createUserDesc := "Create a user in the authentication system\nThis affects only the 'authentication' system - the permit database is not altered by this command. "
	createUser := app.AddCommand("createuser", createUserDesc, "identity", "password")
	createUser.AddValueOption("mobile", "number", "Mobile number (cell phone)")
	createUser.AddValueOption("firstname", "text", "First name")
	createUser.AddValueOption("lastname", "text", "Last name")
	createUser.AddValueOption("username", "text", "Username")
	createUser.AddValueOption("telephone", "text", "Telephone number")
	createUser.AddValueOption("remarks", "text", "Remarks")

	app.AddCommand("killsessions", "Erase all sessions belonging to a particular user\nWarning! The running server maintains a cache of "+
		"sessions, so you must stop the server, run this command, and then start the server again to kill sessions correctly.", "identity")
	app.AddCommand("setpassword", "Set a user's password in Authaus", "identity", "password")
	app.AddCommand("resetpassword", "Send a password reset email", "identity")
	app.AddCommand("setgroup", "Add or modify a group\nThe list of roles specified replaces the existing roles completely.", "groupname", "...role")
	app.AddCommand("renameuser", "Rename a user\nThe user will be logged out of any current sessions", "old", "new")
	app.AddCommand("permgroupadd", "Add a group to a permit", "identity", "groupname")
	app.AddCommand("permgroupdel", "Remove a group from a permit", "identity", "groupname")
	app.AddCommand("permshow", "Show the groups of a permit", "identity")
	app.AddCommand("showidentities", "Show a list of all identities and the groups that they belong to")
	app.AddCommand("showroles", "Show a list of all roles")
	app.AddCommand("showgroups", "Show a list of all groups")
	app.AddCommand("run", "Run the service\nThis will automatically detect if it's being run from the Windows Service dispatcher, and if so, "+
		"launch as a Windows Service. Otherwise, this runs in the foreground, and returns with an error code of 1. When running in the foreground, "+
		"log messages are still sent to the logfile (not to the console).")

	app.AddValueOption("c", "configfile", "Specify the imqsauth config file. A pseudo file called "+auth.TestConfig1+" is "+
		"used by the REST test suite to load a test configuration. This option is mandatory.")

	app.AddBoolOption("nosvc", "Do not try to run as a Windows Service. Normally, the 'run' command detects whether this is an "+
		"'interactive session', and if not interactive, runs as a Windows Service. Specifying -nosvc forces us to launch as a regular process.")

	app.Run()
}

func exec(cmd string, args []string, options cli.OptionSet) int {

	// panic(string) to show an error message.
	// panic(error) will show a stack trace
	defer func() {
		if ex := recover(); ex != nil {
			switch err := ex.(type) {
			case error:
				fmt.Printf("%v\n", err)
				trace := make([]byte, 1024)
				runtime.Stack(trace, false)
				fmt.Printf("%s\n", trace)
			case string:
				if err != "" {
					fmt.Printf("%v\n", err)
				}
			default:
				fmt.Printf("%v\n", ex)
			}
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}()

	ic := &auth.ImqsCentral{}
	ic.Config = &auth.Config{}

	configFile := options["c"]

	// Try test config first; otherwise load real config
	isTestConfig := auth.LoadTestConfig(ic, configFile)
	if !isTestConfig {
		if err := ic.Config.LoadFile(configFile); err != nil {
			panic(fmt.Sprintf("Error loading config file '%v': %v", configFile, err))
		}

		// Detects if service is inside Docker, rewrite HTTP configurations
		if isRunningOnLinuxOutsideOfDocker() {
			ic.Config.MakeOutsideDocker()
		}
	}

	handler := func() error {
		err := ic.RunHttp()
		ic.Central.Close()
		return err
	}

	handlerNoRetVal := func() {
		handler()
	}

	// "createdb" is different to the other command.
	// We cannot initialize an authaus Central object until the DB has been created.
	// The "run" command already creates a new Central object.
	createCentral := cmd != "createdb" && !isTestConfig

	if createCentral {
		var err error
		ic.Central, err = authaus.NewCentralFromConfig(&ic.Config.Authaus)
		if err != nil {
			panic(err)
		}
		defer ic.Central.Close()
	}

	if !isTestConfig {
		// Run migrations
		createDB(&ic.Config.Authaus)
	}

	// Setup audit service
	if ic.Central != nil {
		ic.Central.Auditor = auth.NewIMQSAuditor(ic.Central.Log)
	}

	if ic.Central != nil {
		ic.Central.LockingPolicy = ic
	}

	success := false
	switch cmd {
	case "createdb":
		success = createDB(&ic.Config.Authaus)
	case "createuser":
		success = createUser(ic, options, args[0], args[1])
	case "killsessions":
		success = killSessions(ic, args[0])
	case "permgroupadd":
		success = permGroupAddOrDel(ic, args[0], args[1], true)
	case "permgroupdel":
		success = permGroupAddOrDel(ic, args[0], args[1], false)
	case "permshow":
		success = permShow(ic, 0, args[0])
	case "resetauthgroups":
		if err := auth.ResetAuthGroups(ic); err != nil {
			ic.Central.Log.Errorf("Error: %v\n", err)
		} else {
			success = true
		}
	case "rollbackgroups":
		if err := auth.RollbackUnwantedGroups(ic); err != nil {
			ic.Central.Log.Errorf("RollbackUnwantedGroups failed: %v", err)
		} else {
			success = true
		}
	case "run":
		// Reset auth groups, always
		if err := auth.ResetAuthGroups(ic); err != nil {
			ic.Central.Log.Errorf("ResetAuthGroups failed: %v\n", err)
		}

		if err := auth.RollbackUnwantedGroupsOnce(ic); err != nil {
			ic.Central.Log.Errorf("RollbackUnwantedGroups failed: %v", err)
		}

		// Create initial user if there is one.
		user := os.Getenv("IMQS_INIT_USER")
		pass := os.Getenv("IMQS_INIT_PASS")
		if ic.Config.IsContainer() && user != "" && pass != "" {
			// first check if the user already exists
			_, err := ic.Central.GetUserFromIdentity(user)
			if err != nil {
				ic.Central.Log.Errorf("Creating initial admin user: %v\n", user)
				createUser(ic, map[string]string{}, user, pass)
				permGroupAddOrDel(ic, user, "admin", true)
				permGroupAddOrDel(ic, user, "enabled", true)
			}
		}
		if options.Has("nosvc") || !service.RunAsService(handlerNoRetVal) {
			success = false
			fmt.Print(handler())
		}
	case "setgroup":
		success = setGroup(ic, args[0], args[1:])
	case "setpassword":
		success = setPassword(ic, args[0], args[1])
	case "resetpassword":
		success = resetPassword(ic, args[0])
	case "renameuser":
		success = renameUser(ic, args[0], args[1])
	case "showgroups":
		success = showAllGroups(ic)
	case "showidentities":
		success = showAllIdentities(ic)
	case "showroles":
		showAllRoles()
		success = true
	}

	if !success {
		return 1
	}
	return 0
}

func createDB(config *authaus.Config) bool {
	if err := authaus.SqlCreateDatabase(&config.DB); err != nil {
		fmt.Printf("Error creating database: %v", err)
		return false
	}

	if err := authaus.RunMigrations(&config.DB); err != nil {
		fmt.Printf("Error running migrations: %v", err)
		return false
	}
	return true
}

func resetGroup(ic *auth.ImqsCentral, group *authaus.AuthGroup) bool {
	if existing, eget := ic.Central.GetRoleGroupDB().GetByName(group.Name); eget == nil {
		group.ID = existing.ID
		existing.PermList = group.PermList
		if eupdate := ic.Central.GetRoleGroupDB().UpdateGroup(existing); eupdate == nil {
			fmt.Printf("Group %v updated\n", group.Name)
			return true
		} else {
			fmt.Printf("Error updating group of %v: %v\n", group.Name, eupdate)
		}
	} else if strings.Index(eget.Error(), authaus.ErrGroupNotExist.Error()) == 0 {
		if ecreate := ic.Central.GetRoleGroupDB().InsertGroup(group); ecreate == nil {
			ic.Central.Log.Infof("Group %v created\n", group.Name)
			return true
		} else {
			ic.Central.Log.Errorf("Error inserting group %v: %v\n", group.Name, ecreate)
		}
	} else {
		ic.Central.Log.Errorf("Error updating (retrieving) group %v: %v\n", group.Name, eget)
	}
	return false
}

// add or remove an identity (e.g. user) to or from a group
func permGroupAddOrDel(ic *auth.ImqsCentral, identity string, groupname string, isAdd bool) (success bool) {
	user, eUserId := ic.Central.GetUserFromIdentity(identity)
	if eUserId != nil {
		ic.Central.Log.Errorf("Error retrieving userid for identity: %v\n", identity)
		return false
	}
	perm, eGetPermit := ic.Central.GetPermit(user.UserId)
	if eGetPermit != nil && strings.Index(eGetPermit.Error(), authaus.ErrIdentityPermitNotFound.Error()) == 0 {
		// Tolerate a non-existing identity. We are going to create the permit for this identity.
		perm = &authaus.Permit{}
	} else if eGetPermit != nil {
		ic.Central.Log.Errorf("Error retrieving permit: %v\n", eGetPermit)
		return false
	}

	if group, eGetGroup := ic.Central.GetRoleGroupDB().GetByName(groupname); eGetGroup == nil {
		if groups, eDecode := authaus.DecodePermit(perm.Roles); eDecode == nil {
			haveGroup := false
			for i, gid := range groups {
				if gid == group.ID && !isAdd {
					groups = append(groups[0:i], groups[i+1:]...)
				} else if gid == group.ID && isAdd {
					haveGroup = true
				}
			}
			if !haveGroup && isAdd {
				groups = append(groups, group.ID)
			}
			perm.Roles = authaus.EncodePermit(groups)
			if eSet := ic.Central.SetPermit(user.UserId, perm); eSet == nil {
				ic.Central.Log.Infof("Set permit for %v\n", identity)
				return true
			} else {
				ic.Central.Log.Errorf("Error setting permit: %v\n", eSet)
			}
		} else {
			ic.Central.Log.Errorf("Error decoding permit: %v\n", eDecode)
		}
	} else {
		ic.Central.Log.Errorf("Error retrieving group '%v': %v\n", groupname, eGetGroup)
	}

	return false
}

func permShow(icentral *auth.ImqsCentral, identityColumnWidth int, identity string) (success bool) {
	permStr := ""
	success = false
	user, eUserId := icentral.Central.GetUserFromIdentity(identity)
	if eUserId != nil {
		fmt.Printf("Error retrieving userid for identity: %v\n", identity)
		return false
	}
	groupCache := map[authaus.GroupIDU32]string{}
	if perm, e := icentral.Central.GetPermit(user.UserId); e == nil {
		if groups, eDecode := authaus.DecodePermit(perm.Roles); eDecode == nil {
			groupNames, eGetNames := authaus.GroupIDsToNames(groups, icentral.Central.GetRoleGroupDB(), groupCache)
			if eGetNames != nil && groupNames == nil {
				permStr = fmt.Sprintf("Error converting group IDs to names: %v\n", eGetNames)
			} else {
				errStr := ""
				if eGetNames != nil {
					errStr = fmt.Sprintf("\nWarning: issue converting group IDs to names : %v\n", eGetNames)
				}
				sort.Strings(groupNames)
				permStr = errStr + strings.Join(groupNames, ",")
				success = true
			}
		} else {
			permStr = fmt.Sprintf("Error decoding permit: %v\n", eDecode)
		}
	} else {
		permStr = fmt.Sprintf("Error retrieving permit: %v\n", e)
	}
	fmtStr := fmt.Sprintf("%%-%vv  %%v\n", identityColumnWidth)
	fmt.Printf(fmtStr, identity, permStr)
	return
}

func showAllGroups(icentral *auth.ImqsCentral) bool {
	groups, err := icentral.Central.GetRoleGroupDB().GetGroups()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return false
	}

	longestName := 0
	for _, group := range groups {
		if len(group.Name) > longestName {
			longestName = len(group.Name)
		}
	}
	formatStr := fmt.Sprintf("%%-%vv  %%v\n", longestName)

	fmt.Printf(formatStr, "group", "roles")
	fmt.Printf(formatStr, "-----", "-----")

	for _, group := range groups {
		roles := []string{}
		for _, perm := range group.PermList {
			roles = append(roles, auth.PermissionsTable[perm])
		}
		sort.Strings(roles)
		fmt.Printf(formatStr, group.Name, strings.Join(roles, " "))
	}
	return true
}

func showAllRoles() {
	roles := []string{}
	for _, name := range auth.PermissionsTable {
		roles = append(roles, name)
	}
	sort.Strings(roles)
	for _, name := range roles {
		fmt.Printf("%v\n", name)
	}
}

func showAllIdentities(icentral *auth.ImqsCentral) bool {
	users, err := icentral.Central.GetAuthenticatorIdentities(authaus.GetIdentitiesFlagNone)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return false
	}

	longestName := 0
	for _, user := range users {
		if len(user.Email) > longestName {
			longestName = len(user.Email)
		}
	}

	for _, user := range users {
		permShow(icentral, longestName, user.Email)
	}

	return true
}

func setGroup(icentral *auth.ImqsCentral, groupName string, roles []string) bool {
	perms := []authaus.PermissionU16{}
	nameToPerm := auth.PermissionsTable.Inverted()

	for _, pname := range roles {
		if perm, ok := nameToPerm[pname]; ok {
			perms = append(perms, perm)
			// fmt.Printf("Added permission : %-25v [%v]\n", pname, perm)
		} else {
			panic(fmt.Sprintf("Permission '%v' does not exist", pname))
		}
	}

	err := auth.ModifyGroup(icentral, auth.GroupModifySet, groupName, perms)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return false
	}
	return true
}

func createUser(icentral *auth.ImqsCentral, options map[string]string, identity string, password string) bool {

	isEmail, _ := regexp.MatchString("^([\\w-]+(?:\\.[\\w-]+)*)@((?:[\\w-]+\\.)*\\w[\\w-]{0,66})\\.([a-z]{2,6}(?:\\.[a-z]{2})?)$", identity)
	var e error
	nowTime := time.Now().UTC()
	user := authaus.AuthUser{
		Firstname:       options["firstname"],
		Lastname:        options["lastname"],
		Mobilenumber:    options["mobile"],
		Telephonenumber: options["telephone"],
		Remarks:         options["remarks"],
		Created:         nowTime,
		CreatedBy:       0,
		Modified:        nowTime,
		ModifiedBy:      0,
	}
	if isEmail {
		user.Email = identity
		user.Username = options["username"]
	} else {
		user.Email = options["email"]
		user.Username = identity
	}
	_, e = icentral.Central.CreateUserStoreIdentity(&user, password)

	if e == nil {
		var label string
		if isEmail {
			label = "email address"
		} else {
			label = "username"
		}
		fmt.Printf("Created user with %s %v\n", label, identity)
		return true
	} else {
		fmt.Printf("Error creating identity %v: %v\n", identity, e)
		return false
	}
}

func killSessions(icentral *auth.ImqsCentral, identity string) bool {
	user, eUserId := icentral.Central.GetUserFromIdentity(identity)
	if eUserId != nil {
		fmt.Printf("Error retrieving userid for identity: %v\n", identity)
		return false
	}
	if e := icentral.Central.InvalidateSessionsForIdentity(user.UserId); e == nil {
		fmt.Printf("Destroyed all sessions for %v\n", identity)
		return true
	} else {
		fmt.Printf("Error destroying sessions: %v\n", e)
		return false
	}
}

func setPassword(icentral *auth.ImqsCentral, identity string, password string) bool {
	user, eUserId := icentral.Central.GetUserFromIdentity(identity)
	if eUserId != nil {
		fmt.Printf("Error retrieving userid for identity: %v\n", identity)
		return false
	}
	if e := icentral.Central.SetPassword(user.UserId, password); e == nil {
		fmt.Printf("Reset password of %v\n", identity)
		return true
	} else {
		fmt.Printf("Error resetting password: %v\n", e)
		return false
	}
}

func renameUser(ic *auth.ImqsCentral, oldIdent string, newIdent string) bool {
	if e := ic.Central.RenameIdentity(oldIdent, newIdent); e == nil {
		fmt.Printf("Renamed %v to %v\n", oldIdent, newIdent)
		return true
	} else {
		ic.Central.Log.Errorf("Error renaming: %v\n", e)
		return false
	}
}

func resetPassword(ic *auth.ImqsCentral, identity string) bool {
	user, eUserId := ic.Central.GetUserFromIdentity(identity)
	if eUserId != nil {
		ic.Central.Log.Errorf("Error retrieving userid for identity: %v\n", identity)
		return false
	}
	code, msg := ic.ResetPasswordStart(user.UserId, false)
	if code == 200 {
		fmt.Printf("Message sent\n")
		return true
	} else {
		ic.Central.Log.Errorf("Error %v %v\n", code, msg)
		return false
	}
}
