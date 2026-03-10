class GitsliceError(Exception):
    """Base class for SDK errors."""


class AuthenticationError(GitsliceError):
    """Raised when the server rejects authentication."""


class PermissionDeniedError(GitsliceError):
    """Raised when the caller lacks permission for the operation."""


class ValidationError(GitsliceError):
    """Raised when the request is invalid."""


class NotFoundError(GitsliceError):
    """Raised when the requested resource does not exist."""


class ConflictError(GitsliceError):
    """Raised when the server reports a conflicting state."""


class ServerError(GitsliceError):
    """Raised when the server returns an unexpected error."""
