# Regional workers pull jobs from the API

Workers at each vantage point ask the central API for jobs over outbound HTTPS instead of the API calling them. Vantage points run on more than one provider (Vultr plus Oracle Cloud for Bogotá), and pulling means workers need no open ports or private network, and adding a provider or region is just running the same binary there; we accept slightly less control over dispatch timing.
