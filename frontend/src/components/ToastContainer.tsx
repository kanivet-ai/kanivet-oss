import './ToastContainer.css';

interface ToastContainerProps {
  children: React.ReactNode;
}

const ToastContainer = ({ children }: ToastContainerProps) => {
  return <div className="toast-container">{children}</div>;
};

export default ToastContainer;

