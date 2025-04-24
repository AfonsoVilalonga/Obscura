
# Project Setup and Running Instructions

This project involves multiple components. Follow the instructions below to set up and run each part of the system.

## Prerequisites

Before running the project, ensure you have the following installed:

- **Go**: Version 1.21.2
- **Python**: Version 3.10.1
- **Selenium**
- **Browsers**:
  - Chrome
  - Firefox
- **Browser Drivers**:
  - ChromeDriver
  - GeckoDriver

---

## Order of Execution

To properly run the system, follow this order:

1. **Broker**
2. **Bridge**
3. **Client**
4. **Proxy**

---

## Running the Broker

1. Navigate to the **Broker** folder.
 
2. Build the Go application:
   ```bash
   go build .
   ```

3. Run the executable:
   ```bash
   ./broker
   ```

---

## Running the Bridge

1. Navigate to the **Bridge** folder.

2. Build the Go application:
   ```bash
   go build .
   ```

3. Run the executable:
   ```bash
   ./bridge
   ```

---

## Running the Pion Client

1. Navigate to the **Client** folder.

2. Build the Go application:
   ```bash
   go build .
   ```

3. Run the executable:
   ```bash
   ./client
   ```

---

## Running the Pion Proxy

1. Navigate to the **Proxy** folder.

2. Build the Go application:
   ```bash
   go build .
   ```

3. Run the executable:
   ```bash
   ./proxy
   ```

---

## Running the Browser Client

1. Navigate to the **ClientBrowser** folder.

2. Build the Go application:
   ```bash
   go build .
   ```

3. Run the executable:
   ```bash
   ./client_browser
   ```

4. In the **Node-Server** folder:
   ```bash
   cd Node-Server
   node index.js
   ```

5. In the **Obscura/Selenium** folder:
   - **For Firefox**:
     ```bash
     py firefoxC.py
     ```
   - **For Chrome**:
     ```bash
     py chromeC.py
     ```

---

## Running the Browser with Firefox

1. In the **Proxy-Web** folder:
   ```bash
   node index.js
   ```

2. In the **Obscura/Selenium** folder:
   - **For Firefox**:
     ```bash
     py firefoxP.py
     ```
   - **For Chrome**:
     ```bash
     py chromeP.py
     ```

---

## Notes
- Configurations for ports and addresses are possible in the **Configuration** folders inside each component’s folder.
- To use a Pion instance, a video file in **IVF** format and an audio file in **OGG** format must be available in a folder called **Media** at the root of the project (location is user configurable).
- To use the Browser instances, an **mp4** video file must be available in the `/ClientBrowser/Node-Server/videos` folder for the client browser instance and in the `/Proxy-Web/videos` folder for the proxy browser instance.
- **Bridge** and **Broker** addresses are hardcoded into the **index.html** pages inside the **/Node-Server/** files for the client and proxy instances. To change them, you must edit the code (TODO: change to a configuration file that the HTML page reads from).
- We use **self-signed certificates** for the broker and bridge connections, which means that you must accept the self-signed certificates as trusted in the browser. This can be done by running the Selenium script to open the browser and accessing the broker and bridge addresses (e.g., https://bridge_address:port) in the open browser.


