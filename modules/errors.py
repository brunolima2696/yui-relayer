class YuiError(RuntimeError):
    """Base error presented by the YUI orchestration CLI."""


class ConfigError(YuiError):
    """Invalid declarative configuration."""


class DockerError(YuiError):
    """Failure while interacting with Docker."""


class RelayerError(YuiError):
    """Failure while configuring or running YUI."""
