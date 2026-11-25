## Unreleased

### Fixed
- [FIX|MAJOR] Pause the spinner and wait for an explicit y/n at the remote gate
- [FIX|MAJOR] Stop offering mutating tools before the plan is approved
- [FIX|MEDIUM] Draw the answer panel after the stream when a rewind is not possible
- [ADD|MINOR] Retry a 429 once when the provider asks for 15 seconds or less
- [ADD|MINOR] Implement /remote allow to reset a declined remote decision

## v0.4.0

### Added
- [ADD|MINOR] Price the turn from the serving model
- [ADD|MINOR] Carry the serving model's pricing in agent stats
- [ADD|MAJOR] Report remote models in /doctor
- [ADD|MEDIUM] Check every provider preset field
- [ADD|MINOR] Mark remote models in the welcome data
- [ADD|MEDIUM] Read remote confirmation through the REPL editor
- [ADD|MEDIUM] Gate remote calls on the privacy settings
- [ADD|MAJOR] Add /set key and provider presets
- [ADD|MEDIUM] Send the bearer key and model quirks
- [ADD|MINOR] Add the privacy settings to the key table
- [ADD|MEDIUM] Add API-key provider presets
- [ADD|MEDIUM] Store API keys outside the config file
- [ADD|MEDIUM] Classify local and remote model hosts
- [ADD|MINOR] Add per-model auth, quirks and privacy settings
- [ADD|MINOR] Document the safety model in its own page
- [ADD|MAJOR] Check the command page slash table
- [ADD|MAJOR] Document commands in their own page
- [ADD|MEDIUM] Check the configuration page tables
- [ADD|MAJOR] Document configuration in its own page

### Changed
- [UPDATE|MINOR] Record the remote model limitations
- [UPDATE|MEDIUM] Add a cloud models section to the README
- [UPDATE|MEDIUM] Document cloud models in the configuration guide
- [UPDATE|MINOR] Feed the turn pricing into the stats lines
- [UPDATE|MEDIUM] Add the remote turn cost to the stats lines
- [UPDATE|MINOR] Feed the footer the remote main model
- [UPDATE|MINOR] Mark a remote model in the footer
- [UPDATE|MINOR] Mark remote endpoints in /status
- [UPDATE|MEDIUM] Mark the remote model in the welcome goldens
- [UPDATE|MINOR] Paint the remote marker on welcome models
- [UPDATE|MINOR] Wire the REPL remote prompt in Run
- [UPDATE|MAJOR] Cap the set menu to the terminal height
- [UPDATE|MINOR] Record the privacy rows in the set menu
- [UPDATE|MINOR] Drop the hidden-key menu filter
- [UPDATE|MINOR] Show the privacy keys in the set menu
- [UPDATE|MINOR] Mark the Anthropic preset experimental
- [UPDATE|MINOR] Print the Anthropic preset warning
- [UPDATE|MINOR] Remember the run --yes for remote gating
- [UPDATE|MEDIUM] Gate remote clients and feed the token cap
- [UPDATE|MINOR] Gate the reach probe like the client
- [UPDATE|MEDIUM] Refuse plain-HTTP keys and mask echoed keys
- [UPDATE|MEDIUM] Refuse a remote model before any request
- [UPDATE|MINOR] Expose the endpoint host for messages
- [UPDATE|MINOR] Document API-key providers
- [UPDATE|MEDIUM] Document API-key models
- [UPDATE|MEDIUM] Regenerate the sample config
- [UPDATE|MINOR] Record the privacy keys in config show
- [UPDATE|MINOR] Keep hidden settings out of the menu
- [UPDATE|MEDIUM] Apply model auth and quirks at startup
- [UPDATE|MEDIUM] Apply model auth and quirks to clients
- [UPDATE|MINOR] Send the bearer key on the probe
- [UPDATE|MINOR] Persist the new model fields with /set
- [UPDATE|MEDIUM] Read and write the new model fields
- [UPDATE|MINOR] Keep only the version, license, Go and models badges
- [UPDATE|MAJOR] Rewrite the README for 0.3.0
- [UPDATE|MAJOR] Keep the README image check on local images

### Fixed
- [FIX|MINOR] Size the set-menu test to the menu height

