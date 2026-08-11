package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedPasswordUser stands up an isolated in-memory DB holding one user whose
// password is already hashed, mirroring a real account.
func seedPasswordUser(t *testing.T, plaintext string) int {
	t.Helper()
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}))

	hashed := ""
	if plaintext != "" {
		hashed, err = common.Password2Hash(plaintext)
		require.NoError(t, err)
	}
	user := &model.User{
		Id: 7, Username: "test-user", Password: hashed,
		Group: "default", Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return user.Id
}

// Changing a display name is not a password operation, so it must not require
// the password. JINN's accounts are passwordless email-OTP: they carry a
// generated password the customer never sees, so any profile edit that demanded
// the original password was impossible to satisfy and failed with 原密码错误.
func TestProfileEditDoesNotRequireThePassword(t *testing.T) {
	id := seedPasswordUser(t, "the-real-password")

	updatePassword, err := checkUpdatePassword("", "", id)
	require.NoError(t, err, "a profile-only edit must not be rejected for lacking a password")
	require.False(t, updatePassword, "no new password was supplied, so nothing should be re-hashed")
}

func TestPasswordChangeStillRequiresTheOriginal(t *testing.T) {
	id := seedPasswordUser(t, "the-real-password")

	_, err := checkUpdatePassword("wrong-password", "a-new-password", id)
	require.Error(t, err, "changing the password with the wrong original must still fail")

	updatePassword, err := checkUpdatePassword("the-real-password", "a-new-password", id)
	require.NoError(t, err)
	require.True(t, updatePassword)
}

// An account with no password set yet (first bind) can still set one.
func TestPasswordCanBeSetWhenNoneExists(t *testing.T) {
	id := seedPasswordUser(t, "")

	updatePassword, err := checkUpdatePassword("", "a-new-password", id)
	require.NoError(t, err)
	require.True(t, updatePassword)
}
