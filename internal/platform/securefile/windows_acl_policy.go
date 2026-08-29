package securefile

func windowsServiceFileSDDL(serviceSID, administratorsSID string) string {
	return "D:P(A;;FA;;;" + serviceSID + ")(A;;FA;;;" + administratorsSID + ")"
}

func windowsServiceDirectorySDDL(serviceSID, administratorsSID string) string {
	return "D:P(A;OICI;FA;;;" + serviceSID + ")(A;OICI;FA;;;" + administratorsSID + ")"
}
