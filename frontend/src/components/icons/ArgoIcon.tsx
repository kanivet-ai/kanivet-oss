const ArgoIcon = ({ className = '', width = 15, height = 15 }: { className?: string; width?: number; height?: number }) => (
  <svg
    width={width}
    height={height}
    viewBox="0 0 24 24"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
    className={className}
  >
    <path
      d="M12 2.2c-.32 0-.63.08-.9.24L3.43 6.92a1.8 1.8 0 0 0-.9 1.56v7.04c0 .64.34 1.24.9 1.56l7.67 4.48c.27.16.58.24.9.24s.63-.08.9-.24l7.67-4.48c.56-.32.9-.92.9-1.56V8.48c0-.64-.34-1.24-.9-1.56L12.9 2.44a1.8 1.8 0 0 0-.9-.24z"
      stroke="currentColor"
      strokeWidth="1.6"
      fill="none"
    />
    <circle cx="12" cy="12" r="3.2" stroke="currentColor" strokeWidth="1.6" fill="none" />
    <path d="M12 5.5v3.3M12 15.2v3.3M5.7 8.7l2.9 1.7M15.4 13.6l2.9 1.7M5.7 15.3l2.9-1.7M15.4 10.4l2.9-1.7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
  </svg>
);

export default ArgoIcon;
