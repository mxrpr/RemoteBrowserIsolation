## Feature description
We create a remote browser isolation related server application.
This first iteration is about creating a simple server app, which is responsible to download a web page via http or https, then send back to a browser via WebRtc

## library for WebRTC
use SIPSorcery

## use case
- administrator start the application on a server, app is listening on a port.
- application must be able to accept many connections from different user's browsers
- user starts a browser, enters the url. The browser connects to our server, which is responsible to download the page, then send back to browser the content via WebRTC
- server has to log all requests it received on INFO level
- server has a configuration file, where the user can set: log level

## Used language
the server is implemented in c# language

