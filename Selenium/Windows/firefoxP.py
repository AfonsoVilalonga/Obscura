from selenium import webdriver
from selenium.webdriver.firefox.service import Service
from selenium.webdriver.firefox.options import Options
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
import time

# Set up Firefox options
firefox_options = Options()
firefox_options.add_argument("--headless")  # Run Firefox in headless mode

# Set Firefox preferences
firefox_options.set_preference("network.stricttransportsecurity.preloadlist", False)
firefox_options.set_preference("security.enterprise_roots.enabled", True)
firefox_options.set_preference("media.autoplay.default", 0)  # 0: Allow autoplay for all media
firefox_options.set_preference("media.autoplay.allow-muted", True)  # Allow muted autoplay
firefox_options.set_preference("media.autoplay.block-webaudio", False)  # Allow web audio autoplay
firefox_options.set_preference("media.peerconnection.ice.loopback", True)
firefox_options.binary_location = "C:\\Program Files\\Mozilla Firefox\\firefox.exe"

# Path to the GeckoDriver (Firefox WebDriver)
geckodriver_path = r"C:\Users\Afonso\Desktop\geckodriver.exe"  # Use raw string or escape backslashes

# Set up the WebDriver
service = Service(geckodriver_path)
driver = webdriver.Firefox(service=service, options=firefox_options)

try:
    # Open the specified URL
    url = "http://localhost:3000"
    driver.get(url)
    print(f"Opened {url} successfully.")

    # Wait until the input element is visible before interacting with it
    # input_element = WebDriverWait(driver, 10).until(
    #     EC.visibility_of_element_located((By.ID, "inputField"))
    # )

    # Block execution by keeping the browser running
    print("Firefox is running in headless mode. Press Ctrl+C to stop.")
    
    while True:
        time.sleep(5)  # Keep the script alive to maintain the browser session
        
        # Fetch the value of the input element
        # entered_value = input_element.get_attribute('value')
        # print(f"Input value: {entered_value}")
        
        # # Fetch the value of the 'bits' element
        # bits_element = driver.find_element(By.ID, 'bits')
        # bits_value = bits_element.get_attribute('value')
        # print(f"Bits value: {bits_value}")

finally:
    # Clean up and close the driver
    driver.quit()
    print("Firefox browser closed.")
