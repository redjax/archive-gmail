# Archive Gmail <!-- omit in toc -->

Little Go app for local archive of Gmail mailbox. Connects to Gmail via IMAP and iterates over whole mailbox and folders, downloading each message.

## Table of Contents <!-- omit in toc -->

- [Requirements](#requirements)
- [Setup](#setup)
- [Build](#build)
- [Install](#install)
- [Authenticate using OAuth2](#authenticate-using-oauth2)
- [OAuth2 client setup](#oauth2-client-setup)
- [Env vars](#env-vars)
- [Authenticate](#authenticate)
- [Docker](#docker)

## Requirements

- [Google app password](https://support.google.com/accounts/answer/185833)

## Setup

Set the following environment variables (if you're using `direnv`, create a `.envrc.local` and export them there, then run `direnv allow`):

- `GMAIL_EMAIL`: Gmail account to sign into
- `GMAIL_PASSWORD`: Your app password, i.e. `"xxxx xxxx xxxx xxxx"`
- `BACKUP_DIR`: The path where messages will be archived locally
- `DRY_RUN`: Connect & validate without downloading anything
- `FOLDERS_ONLY`: (default: "") Optional comma-separated list of folders to download
  - Example: INBOX,[Gmail]/All Mail

## Build

Run [one of the build scripts](./scripts/build), or one of the commands below. Run `mkdir dist` to ensure the output directory exists.

Build with Go:

```shell
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/archive-gmail ./cmd/archive-gmail
```

Build with GoReleaser:

```shell
goreleaser release --snapshot --clean
```

## Install

Place the [binary you built to `dist/archive-gmail[.exe]`](#build) somewhere in your `$PATH`, i.e. `~/.local/bin/archive-gmail` (on Linux). You may have to add this to your `~/.bashrc`:

```text
export PATH="$PATH:$HOME/.local/bin"
```

## Authenticate using OAuth2

You can (and should) authenticate the CLI with OAuth2. First you must setup an OAuth2 client app in Google Cloud, then authenticate the CLI to get a token. After doing this setup the first time, the app will handle refreshing the token.

There is [a separate app](./cmd/authenticate/main.go) to handle authenticating. This app simply logs in with the OAuth2 client and gets a token, or refreshes it.

## OAuth2 client setup

*[Google OAuth client ID & secret](https://developers.google.com/identity/protocols/oauth2)*

- Create a Google Cloud Platform project:
  - Go to the [Google Cloud Console](https://console.cloud.google.com/) and sign in.
  - If you don't have a project, click the "Select a project" dropdown and choose "New Project".
  - Give your project a name and click "Create".
- Enable the Necessary APIs
  - In the Google Cloud Console, make sure your newly created or existing project is selected.
  - Navigate to [APIs & Services > Library](https://console.cloud.google.com/apis/library)
  - Search for the specific API you want to use (e.g., "Gmail API", "Google Drive API", 'Google Calendar API').
    - For Google Directory, make sure that you have the [Admin SDK API](https://console.cloud.google.com/apis/library/admin.googleapis.com) enabled.
  - Click on the API from the search results.
  - Click the "Enable" button. Repeat this for all Google APIs your application will need to access
- Configure the OAuth Consent Screen
  - In the Google Cloud Console, go to [Google Auth Platform](https://console.cloud.google.com/auth/overview)
  - Fill in what is asked and click Create
  - Click 'Create'
  - Go to Auth Branding and fill in the required Information
- Scopes: Select the scopes your application needs. Scopes define the permissions your app is requesting (e.g., read emails, access calendar). Be specific and only request the scopes you absolutely need.
  - Go to [Auth Data Access](https://console.cloud.google.com/auth/scopes)
  - You will at least need `openid`, `userinfo.email`, `userinfo.profile`, `admin.directory.group.readonly` and `admin.directory.user` or `admin.directory.user.readonly`
  - The scopes needed by other Google integrations can be found in the Unified App, for example:
    - [Google Contacts](https://app.unified.to/integrations/googlecontacts?tab=oauth2)
    - [Google Directory](https://app.unified.to/integrations/googledirectory?tab=oauth2)
    - [Gmail](https://app.unified.to/integrations/googlemail?tab=oauth2)
  - Note that if you use `Restricted` or `Sensitive` Scopes you need to contact Unified's Support to setup a CNAME for the redirect URL in order to get your App approved by Google
  - Review the information and click "Save and Continue" through the steps. If you selected "External" and are just testing, you can often leave your app in "Testing" publishing status. If you intend for it to be publicly available, you'll eventually need to "Publish" it and potentially go through verification.
- Create OAuth 2.0 Credentials
  - Go to [Auth Clients](https://console.cloud.google.com/auth/clients) in the Google Cloud Console.
  - Click on "+ Create Client" at the top of the page, Follow the steps
  - Configure the authorized redirect URIs. Get the correct value from the Integration, for example Google Drive
  - Copy your Client ID and Secret
  - Enter these values in your Integrations, for example:
    - [Google](https://app.unified.to/integrations/google)
    - [Gmail](https://app.unified.to/integrations/googlemail?tab=auth)
- Publish your Application
  - Go to [Auth Audience](https://console.cloud.google.com/auth/audience)
  - Click on Publish app
- Once your application is approved and verified it will start showing your application name on the authorization consent screen. Until then it will be showing the 'unified.to' name, which is inferred from the redirect URL

## Env vars

If you're using `direnv`, create a  `.envrc.local` and paste your OAuth2 client ID and secret:

```text
# .envrc.local

export GMAIL_EMAIL="jackenyon@gmail.com"
## Leave this empty or remove it completely when using OAuth2
export GMAIL_PASSWORD=

## Set OAuth2 client ID and secret
export GMAIL_CLIENT_ID=<OAuth2 client ID>
export GMAIL_CLIENT_SECRET=<OAuth2 client secret>

export BACKUP_DIR="mailbox"
export DRY_RUN="false"
## Example: INBOX,[Gmail]/All Mail
export FOLDERS_ONLY=""
export MAX_WORKERS=1
export LOG_LEVEL=INFO

```

## Authenticate

The first time you run the app, if no token file is found it will walk you through the auth flow. You can also run the [`archive-gmail-auth` CLI](./cmd/authenticate/main.go), which exits immediately after finishing authentication.

When you run the app or auth CLI, you will see a URL that you should open in your browser. This will walk you through a Google SSO login, and will redirect to an invalid `http://localhost` URL. Inspect the URL and copy the code in the `&code=...` parameter. Paste that into the CLI and a `token.json` file will be saved on your machine. You are now authenticated and do not have to do this again as long as the `token.json` file exists.

When the app runs, it will automatically refresh the token when required.

## Docker

Before starting, copy `.containers/.env.example` to `.containers/.env`. Edit the file, setting your email account and OAuth2 client ID and secret (or app password).

```text
## Default: golang:1.25-alpine
GO_CONTAINER_IMG=
## Default: alpine:latest
GO_RUNTIME_IMG=

GMAIL_EMAIL="your-email@gmail.com"
GMAIL_PASSWORD="xxxx xxxx xxxx xxxx"
## OAuth2 (remove values if using app password)
GMAIL_CLIENT_ID=<OAuth2 client ID>
GMAIL_CLIENT_SECRET=<OAuth2 client secret>

HOST_BACKUP_DIR="/path/to/archive_gmail/mailbox"
DRY_RUN="false"
## Example: INBOX,[Gmail]/All Mail
FOLDERS_ONLY=""
MAX_WORKERS=1
LOG_LEVEL=INFO

TOKEN_FILE=./token

```

There are Docker containers and a `compose.yml` in the [`.containers/` directory](./.containers). If you are using OAuth2 and have not [authenticated](#authenticate) yet, run the [`get_auth_token.sh` script](./scripts/containers/get_auth_token.sh). This will build and run the auth CLI in a container, and persist the token in `.containers/token/token.json`. The [`compose.yml`](./.containers/compose.yml) expects this path to exist and a `token.json` to exist.

After authenticating (if using OAuth2) or pasting your app password in the `.env` file, you can run the container with the [`start_compose.sh` script](./scripts/containers/start_compose.sh).
