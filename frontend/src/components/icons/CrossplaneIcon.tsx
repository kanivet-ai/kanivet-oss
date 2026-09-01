const CrossplaneIcon = ({ className = '', width = 15, height = 15 }: { className?: string; width?: number; height?: number }) => (
  <svg
    width={width}
    height={height}
    viewBox="26 11 48 84"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
    className={className}
  >
    <defs>
      <clipPath id="cp-body">
        <path d="M30.68 32.17c-.03.63-.03 1.25 0 1.88-.01.16-.02.32-.02.48v31.62c0 6.05 4.94 10.99 10.97 10.99h16.5c6.03 0 10.97-4.94 10.97-10.99V34.53c0-.19-.02-.36-.03-.54.03-.6.03-1.21 0-1.82-.46-10.22-8.88-18.37-19.19-18.37s-18.73 8.15-19.2 18.37z" />
      </clipPath>
    </defs>
    <path d="M30.68 32.17c-.03.63-.03 1.25 0 1.88-.01.16-.02.32-.02.48v31.62c0 6.05 4.94 10.99 10.97 10.99h16.5c6.03 0 10.97-4.94 10.97-10.99V34.53c0-.19-.02-.36-.03-.54.03-.6.03-1.21 0-1.82-.46-10.22-8.88-18.37-19.19-18.37s-18.73 8.15-19.2 18.37z" stroke="currentColor" strokeWidth="4" fill="none" />
    <rect x="46" y="75" width="8" height="18" rx="2" fill="currentColor" />
    <g clipPath="url(#cp-body)">
      <line x1="15" y1="55" x2="55" y2="15" stroke="currentColor" strokeWidth="8" />
      <line x1="30" y1="70" x2="70" y2="30" stroke="currentColor" strokeWidth="8" />
      <line x1="45" y1="85" x2="85" y2="45" stroke="currentColor" strokeWidth="8" />
    </g>
  </svg>
);

export default CrossplaneIcon;
