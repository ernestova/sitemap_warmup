# Regional workers pull jobs from the API

Workers at each vantage point ask the central API for jobs over outbound HTTPS instead of the API calling them. Vantage points run on more than one provider (Fly.io plus others in Latin America), and pulling means workers need no open ports and adding a provider or region is just running the same binary there; we accept slightly less control over dispatch timing.
