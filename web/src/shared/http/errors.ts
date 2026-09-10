export class ApiError<T = unknown> extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly data?: T,
    readonly retryAfterSeconds?: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export class NetworkError extends Error {
  constructor() {
    super('NETWORK_ERROR')
    this.name = 'NetworkError'
  }
}

export class RequestCancelledError extends Error {
  constructor() {
    super('REQUEST_CANCELLED')
    this.name = 'RequestCancelledError'
  }
}

export class InvalidResponseError extends Error {
  constructor() {
    super('INVALID_API_RESPONSE')
    this.name = 'InvalidResponseError'
  }
}

export class InvalidRequestPathError extends Error {
  constructor() {
    super('INVALID_API_PATH')
    this.name = 'InvalidRequestPathError'
  }
}
