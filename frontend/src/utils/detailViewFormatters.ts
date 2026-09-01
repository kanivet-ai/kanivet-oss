export const formatBytes = (bytes: number | string): string => {
  const numBytes = typeof bytes === 'string' ? parseInt(bytes) : bytes;
  if (!numBytes || numBytes === 0) return '0 Bytes';
  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(numBytes) / Math.log(k));
  return parseFloat((numBytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
};

export const formatCPU = (cpu: string): string => {
  if (!cpu) return '0';
  if (cpu.endsWith('m')) return cpu;
  const cores = parseFloat(cpu);
  return `${cores * 1000}m`;
};

export const parseCPUToMillicores = (cpu: string): number => {
  if (!cpu) return 0;
  if (cpu.endsWith('m')) {
    return parseFloat(cpu);
  }
  const cores = parseFloat(cpu);
  return cores * 1000;
};

export const parseMemoryToBytes = (memory: string): number => {
  if (!memory) return 0;
  const value = parseFloat(memory);
  if (memory.endsWith('Ki')) return value * 1024;
  if (memory.endsWith('Mi')) return value * 1024 * 1024;
  if (memory.endsWith('Gi')) return value * 1024 * 1024 * 1024;
  if (memory.endsWith('Ti')) return value * 1024 * 1024 * 1024 * 1024;
  return value;
};

export const detectFormat = (value: string): string => {
  const trimmed = value.trim();

  if (trimmed.startsWith('-----BEGIN') && trimmed.includes('-----END')) {
    return 'certificate';
  }

  if ((trimmed.startsWith('{') && trimmed.endsWith('}')) ||
      (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
    try {
      JSON.parse(trimmed);
      return 'json';
    } catch {}
  }

  if (trimmed.includes(':\n') || trimmed.includes(': ') || trimmed.match(/^[\w-]+:\s*$/m)) {
    return 'yaml';
  }

  if (trimmed.match(/^[\w.-]+=.+$/m) && !trimmed.includes(':')) {
    return 'properties';
  }

  if (trimmed.startsWith('<?xml') || (trimmed.startsWith('<') && trimmed.endsWith('>'))) {
    return 'xml';
  }

  return 'text';
};

export const decodeSecret = (encodedValue: string): string => {
  try {
    return atob(encodedValue);
  } catch {
    return '<Invalid Base64>';
  }
};
