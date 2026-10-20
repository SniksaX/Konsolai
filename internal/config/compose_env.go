package config

// ComposeEnvExample is the Docker Compose block appended to .env.example.
// These variables are not config keys and /set does not write them.
func ComposeEnvExample() string {
	return "" +
		"\n" +
		"# Docker Compose. These are not config keys and /set does not write them.\n" +
		"# compose.yaml reads KONSOLAI_DB_PASSWORD from the environment. Use a private value.\n" +
		"KONSOLAI_DB_PASSWORD=change-me\n" +
		"# Uid and gid for the konsolai service. scripts/docker.sh sets these from id.\n" +
		"KONSOLAI_UID=1000\n" +
		"KONSOLAI_GID=1000\n" +
		"# Published host port for the compose database.\n" +
		"KONSOLAI_DB_PORT=55434\n" +
		"# Image stage passed to docker build: base or full.\n" +
		"KONSOLAI_TARGET=base\n" +
		"# Host directory mounted at /work.\n" +
		"KONSOLAI_WORKDIR=.\n" +
		"# Config file mounted at /config/config.yaml.\n" +
		"KONSOLAI_CONFIG_FILE=./config.yaml\n" +
		"# Model id for the optional embedding service. Override to use another model.\n" +
		"KONSOLAI_EMBED_MODEL=BAAI/bge-base-en-v1.5\n" +
		"# Published host port for the optional embedding service.\n" +
		"KONSOLAI_EMBED_PORT=8080\n"
}
