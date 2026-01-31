# Archive Gmail <!-- omit in toc -->

Little Go app for local archive of Gmail mailbox.

## Table of Contents <!-- omit in toc -->

- [Requirements](#requirements)
- [Setup](#setup)
- [OAuth Setup](#oauth-setup)

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

## OAuth Setup

> [!WARN]
> OAuth2 is not implemented yet

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
