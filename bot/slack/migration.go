package slack

// defaultMigrationNote - what approvers do about a database migration when no note is configured
const defaultMigrationNote = "Keel does not run migrations; apply it before approving."

// maxMigrationNoteLength - the longest migration note shown, longer notes are cut
const maxMigrationNoteLength = 300

// migrationWarningText - the warning about a database migration in the change, with the configured note about what
// approvers do about it, or the default note when none is configured
func migrationWarningText(note string) string {
	note = truncateText(note, maxMigrationNoteLength)
	if note == "" {
		return ":warning: Includes a database migration. " + defaultMigrationNote
	}
	return ":warning: Includes a database migration. " + escapeMrkdwn(note)
}
