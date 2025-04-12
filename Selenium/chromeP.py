from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By
import time

# Set up Chrome options
chrome_options = Options()
#chrome_options.add_argument("--headless")  # Run Chrome in headless mode
chrome_options.add_argument("--disable-gpu")  # Disable GPU acceleration
chrome_options.add_argument("--no-sandbox")  # Bypass the sandbox
chrome_options.add_argument("--autoplay-policy=no-user-gesture-required")  # Allow autoplay without user gesture
chrome_options.add_argument("--user-data-dir=/home/bridge/data2")  # Specify user data directory
chrome_options.add_argument("--mute-audio")  # Mute audio
chrome_options.add_argument("--ignore-certificate-errors")  # Ignore certificate errors

# Enable browser logging
chrome_options.set_capability("goog:loggingPrefs", {"browser": "ALL"})  # Capture all logs from the browser

# Path to the ChromeDriver
chromedriver_path = "/media/sf_chromedriver-linux64/chromedriver"  # Use your specified chromedriver path

# Set up the WebDriver
service = Service(chromedriver_path)
driver = webdriver.Chrome(service=service, options=chrome_options)

try:
    # Open the specified URL
    url = "http://localhost:3000"
    driver.get(url)
    print(f"Opened {url} successfully.")

    # Print browser console errors
    logs = driver.get_log("browser")
    if logs:
        print("Browser console logs:")
        for entry in logs:
            print(f"[{entry['level']}] {entry['message']}")
    else:
        print("No console logs found.")

    # Block execution by keeping the browser running
    print("Browser is running. Press Ctrl+C to stop.")
    while True:
        time.sleep(5)
        # input_element = driver.find_element(By.ID, 'inputField')
        # entered_value = input_element.get_attribute('value')
        # print(entered_value)
        # a = driver.find_element(By.ID, 'bits')
        # a = a.get_attribute('value')
        # print(a)# Keep the script alive to maintain the browser session
        
        # input_elementa = driver.find_element(By.ID, 'bitsa')
        # entered_valuea = input_elementa.get_attribute('value')
        # print(entered_valuea)
        
        # input_elements = driver.find_element(By.ID, 'inputFielda')
        # entered_values = input_elements.get_attribute('value')
        # print(entered_values)

finally:
    # Clean up and close the driver
    driver.quit()
    print("Browser closed.")
