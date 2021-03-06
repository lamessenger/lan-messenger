package domains

import (
	"errors"
	"strings"
	"sync"
	"time"

	s "github.com/imyousuf/lan-messenger/application/storage"
	"github.com/imyousuf/lan-messenger/packet"
	"github.com/imyousuf/lan-messenger/profile"
	"github.com/imyousuf/lan-messenger/utils"
)

// ******************** Errors ********************

// InvalidStateError represents an error which prevents an action to go ahead
type InvalidStateError string

// SaveOperationFailedError represents a DB.Save() operation failure
type SaveOperationFailedError string

const (
	// InvalidRenewTimeErrorMsg should be sent when time for new expiry is
	// earlier than current time
	InvalidRenewTimeErrorMsg = "renew time can not be from past"
	// RenewFailureMsg should returned whenever the update to DB fails.
	RenewFailureMsg = "renew session failed"
)

// ******************** User ********************

var userMutex sync.Mutex

// User represents a User of the application
type User struct {
	userProfile profile.UserProfile
	userModel   *s.UserModel
}

func (user *User) persistOrLoad() {
	if user.userProfile != nil {
		userMutex.Lock()
		defer userMutex.Unlock()
		userModel, found := getUserModelByUsername(user.userProfile.GetUsername())
		if found {
			user.userModel = userModel
		} else {
			userModel.Username, userModel.DisplayName, userModel.Email = user.userProfile.GetUsername(),
				user.userProfile.GetDisplayName(), user.userProfile.GetEmail()
			s.GetDB().Create(userModel)
			user.userModel = userModel
		}
	}
}

// GetUserProfile returns the profile.UserProfile of the user
func (user User) GetUserProfile() profile.UserProfile {
	return user.userProfile
}

// IsPersisted returns whether the instance represents a persisted model
func (user User) IsPersisted() bool {
	return !s.GetDB().NewRecord(user.userModel)
}

// AddSession adds a session to the user. One can only add a non-persisted session to a persisted
// user. If a persisted session is added or any session is added to a non-persisted user it would
// panic with InvalidStateError. It would return false if the session already belongs to the user
// from prior or if unexpected error occurs and return true if its successfully added. It will panic
// if duplicate session ID is being tried to save, primarily because that should not happen in general
// so handling of that panic is not expected in normal circumstance.
func (user *User) AddSession(session *Session) bool {
	if !user.IsPersisted() {
		panic(InvalidStateError("Session being added to a user before being persisted"))
	}
	if session.IsPersisted() {
		if session.GetSessionOwner().userModel.ID != user.userModel.ID {
			panic(InvalidStateError("Adding session of another owner!"))
		} else {
			return false
		}
	}
	savePassed := true
	utils.PanicableInvocation(func() {
		session.persistSession(user)
	}, func(err interface{}) {
		if saveError, ok := err.(SaveOperationFailedError); ok {
			panic(saveError)
		}
		savePassed = false
	})
	return savePassed
}

// GetActiveSessions get currently active sessions
func (user *User) GetActiveSessions() []*Session {
	allSessions := getSessionsForUser(user)
	activeSessions := make([]*Session, 0, len(allSessions))
	for _, aSession := range allSessions {
		if !aSession.IsExpired() {
			activeSessions = append(activeSessions, aSession)
		}
	}
	return activeSessions
}

// GetMainSession gets the session with lowest device preference index for the given user
func (user User) GetMainSession() (*Session, bool) {
	sessions := user.GetActiveSessions()
	if len(sessions) <= 0 {
		return &Session{sessionModel: &s.SessionModel{}}, false
	}
	mainSession := sessions[0]
	for _, session := range sessions {
		if session.devicePreferenceIndex < mainSession.devicePreferenceIndex {
			mainSession = session
		}
	}
	return mainSession, true
}

func getUserModelByUsername(username string) (*s.UserModel, bool) {
	userModel := &s.UserModel{}
	newDB := s.GetDB().Where("username = ?", username).First(userModel)
	return userModel, !newDB.RecordNotFound()
}

// NewUser returns a new instance of the User
func NewUser(userProfile profile.UserProfile) *User {
	user := &User{userProfile: userProfile}
	user.persistOrLoad()
	return user
}

func populateUserFromModel(user *User, userModel *s.UserModel) {
	user.userModel = userModel
	user.userProfile = profile.NewUserProfile(userModel.Username, userModel.DisplayName,
		userModel.Email)
}

// GetUserByUsername retrieves the user signified by username
func GetUserByUsername(username string) (*User, bool) {
	user := &User{}
	userModel, found := getUserModelByUsername(username)
	if found {
		populateUserFromModel(user, userModel)
	}
	return user, found
}

// ******************** Session ********************

// Session represents a user's session
type Session struct {
	sessionModel            *s.SessionModel
	user                    *User
	sessionID               string
	devicePreferenceIndex   uint8
	expiryTime              time.Time
	replyToConnectionString string
}

// IsPersisted returns whether the instance represents a persisted model
func (session Session) IsPersisted() bool {
	return !s.GetDB().NewRecord(session.sessionModel)
}

// GetSessionOwner returns the User who owns this session instance
func (session Session) GetSessionOwner() *User {
	return session.user
}

// GetExpiryTime returns the expiry time of this session
func (session Session) GetExpiryTime() time.Time {
	return session.expiryTime
}

// IsExpired evaluates whether the session is expired or not. True is returned if the session is expired
func (session Session) IsExpired() bool {
	return time.Now().After(session.expiryTime)
}

// GetReplyToConnectionString returns the connection string for the given session to send messages to
func (session Session) GetReplyToConnectionString() string {
	return session.replyToConnectionString
}

// IsSelf retrieves whether the current session is of this app itself.
func (session Session) IsSelf() bool {
	return packet.GetCurrentSessionID() == session.sessionID
}

func (session *Session) updateExpiryTime(newExpiryTime time.Time) error {
	if !session.IsPersisted() {
		return errors.New(RenewFailureMsg)
	}
	rowsAffected := s.GetDB().Model(session.sessionModel).
		Updates(s.SessionModel{ExpiryTime: newExpiryTime}).RowsAffected
	if rowsAffected < 1 {
		return errors.New(RenewFailureMsg)
	}
	// This should not ever happen, but if happens the program should exit
	if rowsAffected > 1 {
		panic("data corrupted")
	}
	session.expiryTime = newExpiryTime.Truncate(time.Nanosecond)
	return nil
}

// Renew - as the name suggests, renews the session till the newly specified time
func (session *Session) Renew(newExpiryTime time.Time) error {
	if time.Now().After(newExpiryTime) {
		return errors.New(InvalidRenewTimeErrorMsg)
	}
	return session.updateExpiryTime(newExpiryTime)
}

// SignOff force expires the current session
func (session *Session) SignOff() error {
	return session.updateExpiryTime(time.Now().Add(-100 * time.Millisecond))
}

func (session *Session) persistSession(user *User) {
	sessionModel := session.sessionModel
	sessionModel.UserModelID = user.userModel.ID
	sessionModel.DevicePreferenceIndex = session.devicePreferenceIndex
	sessionModel.ExpiryTime = session.expiryTime
	sessionModel.SessionID = session.sessionID
	sessionModel.ReplyToConnectionString = session.replyToConnectionString
	saveResultDB := s.GetDB().Save(sessionModel)
	if saveResultDB.Error != nil {
		if strings.Contains(saveResultDB.Error.Error(), "UNIQUE constraint failed") {
			panic(SaveOperationFailedError(saveResultDB.Error.Error()))
		} else {
			panic(saveResultDB.Error)
		}
	}
	session.user = user
}

func getSessionFromModel(sessionModel *s.SessionModel) *Session {
	session := &Session{sessionID: sessionModel.SessionID, sessionModel: sessionModel,
		devicePreferenceIndex: sessionModel.DevicePreferenceIndex, expiryTime: sessionModel.ExpiryTime,
		replyToConnectionString: sessionModel.ReplyToConnectionString}
	return session
}

func loadUserFromSession(session *Session) {
	sessionModel := session.sessionModel
	s.GetDB().Model(sessionModel).Related(&sessionModel.UserModel)
	user := &User{}
	populateUserFromModel(user, &sessionModel.UserModel)
	session.user = user
}

func getSessionsForUser(user *User) []*Session {
	sessionModels := []s.SessionModel{}
	s.GetDB().Find(&sessionModels, s.SessionModel{UserModelID: user.userModel.ID})
	sessions := make([]*Session, len(sessionModels), len(sessionModels))
	for index, sessionModel := range sessionModels {
		sessions[index] = getSessionFromModel(&sessionModel)
		sessions[index].user = user
	}
	return sessions
}

// GetSessionBySessionID loads from DB with the matching session id
func GetSessionBySessionID(sessionID string) (*Session, bool) {
	sessionModel := &s.SessionModel{}
	s.GetDB().Where(s.SessionModel{SessionID: sessionID}).First(sessionModel)
	session := getSessionFromModel(sessionModel)
	found := false
	if session.IsPersisted() {
		loadUserFromSession(session)
		found = true
	}
	return session, found
}

// NewSession creates a non-persisted new Session to be added to a user
func NewSession(sessionID string, devicePreferenceIndex uint8, expiryTime time.Time,
	replyTo string) *Session {
	session := &Session{sessionID: sessionID, devicePreferenceIndex: devicePreferenceIndex,
		expiryTime: expiryTime, replyToConnectionString: replyTo, sessionModel: &s.SessionModel{}}
	return session
}
