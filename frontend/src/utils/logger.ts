const isDev = process.env.NODE_ENV !== 'production' || window.electron?.isDev;

type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG';

class Logger {
  private isElectron: boolean;

  constructor() {
    this.isElectron = window.electron !== undefined;
  }

  formatMessage(level: LogLevel, message: string, data?: any): string {
    const timestamp = new Date().toISOString();
    const dataStr = data ? ` ${JSON.stringify(data)}` : '';
    return `[${timestamp}] [${level}] ${message}${dataStr}`;
  }

  log(level: LogLevel, message: string, data?: any): void {
    // In production, only log errors to the Electron log file
    // Skip debug, info, and warn to avoid filling up customer disk space
    if (!isDev && level !== 'ERROR') {
      return;
    }

    const formattedMessage = this.formatMessage(level, message, data);

    // Only write to Electron log file for errors in production, or everything in dev
    if (this.isElectron && window.electron) {
      if (isDev || level === 'ERROR') {
        window.electron.log(formattedMessage);
      }
    }

    // Console output only in development
    if (isDev) {
      switch (level) {
        case 'ERROR':
          console.error(message, data || '');
          break;
        case 'WARN':
          console.warn(message, data || '');
          break;
        case 'INFO':
          console.info(message, data || '');
          break;
        case 'DEBUG':
          console.log(message, data || '');
          break;
        default:
          console.log(message, data || '');
      }
    }
  }

  info(message: string, data?: any): void {
    this.log('INFO', message, data);
  }

  error(message: string, data?: any): void {
    this.log('ERROR', message, data);
  }

  warn(message: string, data?: any): void {
    this.log('WARN', message, data);
  }

  debug(message: string, data?: any): void {
    this.log('DEBUG', message, data);
  }
}

const loggerInstance = new Logger();
export default loggerInstance;
