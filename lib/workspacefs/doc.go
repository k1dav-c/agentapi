// Package workspacefs reads the files of the agent's working directory for
// the chat's file viewer: a file's text (Content), its uncommitted changes
// (Diff, through git commands that run none of the programs a repository
// can configure), a self-contained preview of an HTML file (Preview), a
// file to serve (Open) and the search for a path the agent named (Find).
// What it refuses is an *Error, whose Kind lib/httpapi turns into a status.
package workspacefs
