package cloud

import (
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func clientcmdLoad(path string) (*clientcmdapi.Config, error) {
	return clientcmd.LoadFromFile(path)
}
